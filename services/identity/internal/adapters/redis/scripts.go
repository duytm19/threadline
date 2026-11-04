package redis

import (
	_ "embed"

	goredis "github.com/redis/go-redis/v9"
)

//go:embed lua/exchange_token.lua
var ExchangeTokenScriptSrc string

//go:embed lua/create_family.lua
var CreateFamilyScriptSrc string

//go:embed lua/revoke_family.lua
var RevokeFamilyScriptSrc string

var (
	exchangeTokenScript = goredis.NewScript(ExchangeTokenScriptSrc)
	createFamilyScript  = goredis.NewScript(CreateFamilyScriptSrc)
	revokeFamilyScript  = goredis.NewScript(RevokeFamilyScriptSrc)
)

// ParseLuaResult converts an alternating key-value string slice returned from Lua into a Go map.
func ParseLuaResult(raw interface{}) (map[string]string, error) {
	slice, ok := raw.([]interface{})
	if !ok {
		return nil, ErrUnexpectedLuaResponse
	}

	result := make(map[string]string, len(slice)/2)
	for i := 0; i < len(slice); i += 2 {
		if i+1 >= len(slice) {
			break
		}
		key, ok1 := slice[i].(string)
		val, ok2 := slice[i+1].(string)
		if ok1 && ok2 {
			result[key] = val
		}
	}

	return result, nil
}
