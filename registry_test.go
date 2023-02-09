package beeorm

import (
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
)

func TestRegisterRedisSentinelWithOptions(t *testing.T) {
	registry := &Registry{}
	opt := redis.FailoverOptions{}
	opt.Username = "test_user"
	opt.Password = "test_pass"
	sentinels := []string{"127.0.0.1:23", "127.0.0.1:24"}

	registry.RegisterRedisSentinelWithOptions("my_namespace", opt, 0, sentinels)
	vRegistry, err := registry.Validate()
	assert.NoError(t, err)
	pools := vRegistry.GetRedisPools()
	assert.Len(t, pools, 1)
}