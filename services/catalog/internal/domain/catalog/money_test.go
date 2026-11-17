package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestMoney_Validation(t *testing.T) {
	// Negative amount
	_, err := catalog.NewMoney(-1, "USD")
	assert.ErrorIs(t, err, catalog.ErrNegativeAmount)

	// Invalid currency
	_, err = catalog.NewMoney(100, "")
	assert.ErrorIs(t, err, catalog.ErrInvalidCurrency)

	_, err = catalog.NewMoney(100, "US")
	assert.ErrorIs(t, err, catalog.ErrInvalidCurrency)

	_, err = catalog.NewMoney(100, "US1")
	assert.ErrorIs(t, err, catalog.ErrInvalidCurrency)

	// Valid creation
	m, err := catalog.NewMoney(1050, "usd")
	require.NoError(t, err)
	assert.Equal(t, int64(1050), m.Amount())
	assert.Equal(t, "USD", m.Currency())
	assert.False(t, m.IsZero())
	assert.Equal(t, "1050 USD", m.String())

	// Zero amount
	mZero, err := catalog.NewMoney(0, "EUR")
	require.NoError(t, err)
	assert.True(t, mZero.IsZero())

	// MustNewMoney
	assert.NotPanics(t, func() {
		catalog.MustNewMoney(500, "GBP")
	})
	assert.Panics(t, func() {
		catalog.MustNewMoney(-500, "GBP")
	})
}

func TestMoney_Add(t *testing.T) {
	m1 := catalog.MustNewMoney(100, "USD")
	m2 := catalog.MustNewMoney(250, "USD")
	mDiff := catalog.MustNewMoney(250, "EUR")

	sum, err := m1.Add(m2)
	require.NoError(t, err)
	assert.Equal(t, int64(350), sum.Amount())
	assert.Equal(t, "USD", sum.Currency())

	_, err = m1.Add(mDiff)
	assert.ErrorIs(t, err, catalog.ErrCurrencyMismatch)
}
