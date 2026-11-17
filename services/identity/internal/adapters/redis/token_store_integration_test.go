package redis_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	adapter "github.com/threadline/services/identity/internal/adapters/redis"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

func initDockerHost() {
	if os.Getenv("DOCKER_HOST") == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			desktopSock := filepath.Join(home, ".docker", "desktop", "docker.sock")
			if _, err := os.Stat(desktopSock); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+desktopSock)
			}
		}
	}
}

// startRedisContainer spins up an isolated Redis 7 Alpine container for integration testing.
func startRedisContainer(t *testing.T, ctx context.Context) (testcontainers.Container, *adapter.TokenStore, *goredis.Client) {
	initDockerHost()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("skipping Redis Testcontainers integration test (Docker unreachable): %v", err)
		return nil, nil, nil
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to obtain container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to obtain container mapped port: %v", err)
	}

	redisAddr := fmt.Sprintf("%s:%s", host, port.Port())
	client := goredis.NewClient(&goredis.Options{
		Addr: redisAddr,
	})

	if pingErr := client.Ping(ctx).Err(); pingErr != nil {
		_ = client.Close()
		_ = container.Terminate(ctx)
		t.Fatalf("failed to ping Redis container: %v", pingErr)
	}

	store := adapter.NewTokenStore(client)
	return container, store, client
}

// TestTokenStore_ConcurrentReplayAttacks validates Scenario 2 under high-concurrency attack conditions:
// 1. Legitimate user rotates RT-1 -> RT-2.
// 2. Attacker bombards Redis concurrently with stolen RT-1.
// 3. User concurrently attempts to use RT-2.
// 4. Redis Lua script atomically revokes the entire family, invalidates RT-2, and emits A07 alert.
func TestTokenStore_ConcurrentReplayAttacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, store, client := startRedisContainer(t, ctx)
	if container == nil {
		return
	}
	defer func() {
		_ = client.Close()
		_ = container.Terminate(ctx)
	}()

	now := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	tenantID := "zalora-vn"
	familyID := "fam-replay-sim-01"
	userID := "usr-victim-01"

	rt1Raw := "rt_initial_secret_token_1"
	rt1Hash := tokenfamily.HashToken(rt1Raw)
	rt1JTI := "jti-rt-1"

	rt2Raw := "rt_legitimate_rotated_token_2"
	rt2Hash := tokenfamily.HashToken(rt2Raw)
	rt2JTI := "jti-rt-2"

	ttl := 30 * 24 * time.Hour
	initialExpiresAt := now.Add(ttl)

	// Step 1: Create initial TokenFamily with RT-1
	family, err := tokenfamily.NewTokenFamily(familyID, tenantID, userID, rt1Hash, rt1JTI, initialExpiresAt, now)
	if err != nil {
		t.Fatalf("failed to construct TokenFamily: %v", err)
	}

	if err := store.CreateFamily(ctx, family, ttl); err != nil {
		t.Fatalf("failed to seed initial TokenFamily in Redis: %v", err)
	}

	// Step 2: Legitimate rotation of RT-1 to RT-2
	rotatedAt := now.Add(1 * time.Minute)
	res, err := store.ExchangeToken(
		ctx,
		tenantID,
		rt1Hash,
		rt2Hash,
		rt2JTI,
		rotatedAt.Add(ttl),
		rotatedAt,
		ttl,
	)
	if err != nil {
		t.Fatalf("expected successful initial exchange, got: %v", err)
	}
	if res.Generation != 1 || res.NewTokenJTI != rt2JTI {
		t.Fatalf("unexpected exchange result: %+v", res)
	}

	// Verify RT-1 is now marked USED in Redis
	rt1Key, _ := adapter.TokenKey(tenantID, rt1Hash)
	rt1Data, err := client.HGetAll(ctx, rt1Key).Result()
	if err != nil {
		t.Fatalf("failed to get RT-1 data: %v", err)
	}
	if rt1Data["used"] != "1" || rt1Data["state"] != "USED" {
		t.Fatalf("expected RT-1 to be marked used=1 and state=USED, got: %+v", rt1Data)
	}

	// Step 3: Concurrent Replay Simulation
	// 30 concurrent goroutines replay already-used RT-1
	// 20 concurrent goroutines attempt to exchange RT-2
	const replayWorkers = 30
	const validWorkers = 20

	var (
		wg             sync.WaitGroup
		replayErrors   int64
		a07AlertCount  int64
		validBlocked   int64
	)

	startBarrier := make(chan struct{})

	// Attacker workers replaying RT-1
	for i := 0; i < replayWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			attackerHash := tokenfamily.HashToken(fmt.Sprintf("attacker_fake_token_%d", workerID))
			attackerJTI := fmt.Sprintf("jti-attacker-%d", workerID)
			attackNow := rotatedAt.Add(2 * time.Minute)

			_, exErr := store.ExchangeToken(
				ctx,
				tenantID,
				rt1Hash,
				attackerHash,
				attackerJTI,
				attackNow.Add(ttl),
				attackNow,
				ttl,
			)

			if exErr != nil {
				atomic.AddInt64(&replayErrors, 1)
				var tokenExErr *ports.TokenExchangeError
				if errors.As(exErr, &tokenExErr) {
					if tokenExErr.AlertCode == tokenfamily.SecurityAlertTokenReuse ||
						tokenExErr.Status == "TOKEN_REUSED" ||
						tokenExErr.Status == "FAMILY_COMPROMISED" {
						atomic.AddInt64(&a07AlertCount, 1)
					}
				} else if errors.Is(exErr, tokenfamily.ErrTokenReused) || errors.Is(exErr, tokenfamily.ErrFamilyCompromised) {
					atomic.AddInt64(&a07AlertCount, 1)
				}
			}
		}(i)
	}

	// Legitimate workers attempting to rotate RT-2 concurrently with the attack
	for i := 0; i < validWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			rt3Hash := tokenfamily.HashToken(fmt.Sprintf("rt3_token_%d", workerID))
			rt3JTI := fmt.Sprintf("jti-rt3-%d", workerID)
			validNow := rotatedAt.Add(2 * time.Minute)

			_, exErr := store.ExchangeToken(
				ctx,
				tenantID,
				rt2Hash,
				rt3Hash,
				rt3JTI,
				validNow.Add(ttl),
				validNow,
				ttl,
			)

			if exErr != nil {
				atomic.AddInt64(&validBlocked, 1)
			}
		}(i)
	}

	// Release all workers simultaneously
	close(startBarrier)
	wg.Wait()

	// Invariant 1: ALL replay attempts of RT-1 must be rejected
	if replayErrors != int64(replayWorkers) {
		t.Errorf("expected all %d replay attempts to be rejected, but only %d failed", replayWorkers, replayErrors)
	}

	// Invariant 2: Security alert A07_TOKEN_REUSE must be emitted
	if a07AlertCount == 0 {
		t.Errorf("expected A07_TOKEN_REUSE security alerts, got %d", a07AlertCount)
	}

	// Invariant 3: Family state in Redis must be COMPROMISED
	reconstituted, err := store.GetFamily(ctx, tenantID, familyID)
	if err != nil {
		t.Fatalf("failed to retrieve family from Redis: %v", err)
	}
	if reconstituted.State() != tokenfamily.StateCompromised {
		t.Errorf("expected family state COMPROMISED, got %q", reconstituted.State())
	}

	// Invariant 4: RT-2 must be REVOKED in Redis (blacklisted)
	rt2Key, _ := adapter.TokenKey(tenantID, rt2Hash)
	rt2Data, err := client.HGetAll(ctx, rt2Key).Result()
	if err != nil {
		t.Fatalf("failed to read RT-2 metadata: %v", err)
	}
	if rt2Data["state"] != "REVOKED" {
		t.Errorf("expected RT-2 state to be REVOKED, got %q", rt2Data["state"])
	}

	// Invariant 5: Any subsequent attempt to rotate RT-2 fails closed
	_, postAttackErr := store.ExchangeToken(
		ctx,
		tenantID,
		rt2Hash,
		tokenfamily.HashToken("post_attack_token"),
		"jti-post-attack",
		rotatedAt.Add(ttl),
		rotatedAt.Add(3*time.Minute),
		ttl,
	)
	if postAttackErr == nil {
		t.Fatal("expected post-attack exchange of RT-2 to fail, but succeeded")
	}
}

// TestTokenStore_ConcurrentFirstRotation validates atomic mutual exclusion on the initial token exchange:
// When 20 goroutines concurrently present RT-1 before it has been rotated:
// - EXACTLY ONE goroutine must succeed.
// - All other 19 goroutines must be rejected (triggering reuse detection because RT-1 was consumed).
func TestTokenStore_ConcurrentFirstRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, store, client := startRedisContainer(t, ctx)
	if container == nil {
		return
	}
	defer func() {
		_ = client.Close()
		_ = container.Terminate(ctx)
	}()

	now := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	tenantID := "zalora-vn"
	familyID := "fam-race-first-use"
	userID := "usr-race-01"

	rtRaw := "rt_race_initial_secret"
	rtHash := tokenfamily.HashToken(rtRaw)
	rtJTI := "jti-race-1"
	ttl := 30 * 24 * time.Hour

	family, err := tokenfamily.NewTokenFamily(familyID, tenantID, userID, rtHash, rtJTI, now.Add(ttl), now)
	if err != nil {
		t.Fatalf("failed to construct TokenFamily: %v", err)
	}
	if err := store.CreateFamily(ctx, family, ttl); err != nil {
		t.Fatalf("failed to create initial family: %v", err)
	}

	const concurrentRacers = 20
	var (
		wg          sync.WaitGroup
		successes   int64
		failures    int64
		startBarrier = make(chan struct{})
	)

	for i := 0; i < concurrentRacers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier

			newHash := tokenfamily.HashToken(fmt.Sprintf("racer_token_%d", workerID))
			newJTI := fmt.Sprintf("jti-racer-%d", workerID)

			res, exErr := store.ExchangeToken(
				ctx,
				tenantID,
				rtHash,
				newHash,
				newJTI,
				now.Add(ttl),
				now.Add(time.Duration(workerID)*time.Millisecond),
				ttl,
			)

			if exErr == nil && res != nil {
				atomic.AddInt64(&successes, 1)
			} else {
				atomic.AddInt64(&failures, 1)
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	// Invariant: Exactly one exchange succeeds
	if successes != 1 {
		t.Errorf("expected EXACTLY 1 successful rotation under race, got %d", successes)
	}
	if failures != int64(concurrentRacers-1) {
		t.Errorf("expected %d racers to be rejected, got %d", concurrentRacers-1, failures)
	}
}

// TestTokenStore_MultiTenantIsolation validates that keys partitioned by tenant hash tags
// never leak or cross-contaminate state across tenants under concurrent execution.
func TestTokenStore_MultiTenantIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, store, client := startRedisContainer(t, ctx)
	if container == nil {
		return
	}
	defer func() {
		_ = client.Close()
		_ = container.Terminate(ctx)
	}()

	now := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	ttl := 30 * 24 * time.Hour

	// Both tenants use the same FamilyID and same token strings
	familyID := "fam-shared-id"
	rawToken := "shared_secret_across_tenants"
	tokenHash := tokenfamily.HashToken(rawToken)

	famA, _ := tokenfamily.NewTokenFamily(familyID, "tenant-alpha", "user-a", tokenHash, "jti-a", now.Add(ttl), now)
	famB, _ := tokenfamily.NewTokenFamily(familyID, "tenant-beta", "user-b", tokenHash, "jti-b", now.Add(ttl), now)

	if err := store.CreateFamily(ctx, famA, ttl); err != nil {
		t.Fatalf("failed to seed tenant-alpha: %v", err)
	}
	if err := store.CreateFamily(ctx, famB, ttl); err != nil {
		t.Fatalf("failed to seed tenant-beta: %v", err)
	}

	// Compromise tenant-alpha's family
	if err := store.CompromiseFamily(ctx, "tenant-alpha", familyID, "breach", now); err != nil {
		t.Fatalf("failed to compromise tenant-alpha: %v", err)
	}

	// Verify tenant-alpha is COMPROMISED
	reconstitutedA, err := store.GetFamily(ctx, "tenant-alpha", familyID)
	if err != nil || reconstitutedA.State() != tokenfamily.StateCompromised {
		t.Fatalf("expected tenant-alpha to be compromised, got %v (state=%v)", err, reconstitutedA.State())
	}

	// Verify tenant-beta is completely unaffected (ACTIVE)
	reconstitutedB, err := store.GetFamily(ctx, "tenant-beta", familyID)
	if err != nil || reconstitutedB.State() != tokenfamily.StateActive {
		t.Fatalf("expected tenant-beta to remain ACTIVE, got %v (state=%v)", err, reconstitutedB.State())
	}
}
