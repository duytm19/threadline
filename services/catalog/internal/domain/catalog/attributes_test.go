package catalog_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestAttributes_Getters(t *testing.T) {
	raw := map[string]any{
		"material": "100% Cashmere",
		"pockets":  4,
		"pockets_float": 4.0,
		"pockets_64": int64(4),
		"washable": true,
	}

	attrs := catalog.NewAttributes(raw)

	// String
	str, ok := attrs.GetString("material")
	assert.True(t, ok)
	assert.Equal(t, "100% Cashmere", str)

	_, ok = attrs.GetString("non_existent")
	assert.False(t, ok)

	_, ok = attrs.GetString("pockets")
	assert.False(t, ok)

	// Int
	val, ok := attrs.GetInt("pockets")
	assert.True(t, ok)
	assert.Equal(t, int64(4), val)

	val, ok = attrs.GetInt("pockets_float")
	assert.True(t, ok)
	assert.Equal(t, int64(4), val)

	val, ok = attrs.GetInt("pockets_64")
	assert.True(t, ok)
	assert.Equal(t, int64(4), val)

	_, ok = attrs.GetInt("material")
	assert.False(t, ok)

	_, ok = attrs.GetInt("non_existent")
	assert.False(t, ok)

	// Bool
	b, ok := attrs.GetBool("washable")
	assert.True(t, ok)
	assert.True(t, b)

	_, ok = attrs.GetBool("material")
	assert.False(t, ok)

	_, ok = attrs.GetBool("non_existent")
	assert.False(t, ok)

	// Defensive copy
	m := attrs.Map()
	m["injected"] = "malicious"
	_, exists := attrs.GetString("injected")
	assert.False(t, exists)
}

func TestAttributes_ZeroValue(t *testing.T) {
	var zeroAttrs catalog.Attributes
	assert.Empty(t, zeroAttrs.Map())
	_, ok := zeroAttrs.GetString("any")
	assert.False(t, ok)
	_, ok = zeroAttrs.GetInt("any")
	assert.False(t, ok)
	_, ok = zeroAttrs.GetBool("any")
	assert.False(t, ok)
}

func TestAttributes_JSON(t *testing.T) {
	raw := map[string]any{
		"color": "blue",
		"count": float64(2),
	}
	attrs := catalog.NewAttributes(raw)

	data, err := json.Marshal(attrs)
	require.NoError(t, err)

	var unmarshaled catalog.Attributes
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	col, ok := unmarshaled.GetString("color")
	assert.True(t, ok)
	assert.Equal(t, "blue", col)

	var emptyAttrs catalog.Attributes
	emptyData, err := json.Marshal(emptyAttrs)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(emptyData))

	var invalidAttrs catalog.Attributes
	err = json.Unmarshal([]byte("not json"), &invalidAttrs)
	assert.Error(t, err)
}
