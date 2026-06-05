package configcenter

import (
	"fmt"

	configurator "github.com/zeromicro/go-zero/core/configcenter"
	"github.com/zeromicro/go-zero/core/configcenter/subscriber"
)

// NewEtcdSubscriber creates an etcd subscriber for the given endpoints and key.
// The key is the exact etcd key where the config value is stored.
func NewEtcdSubscriber(endpoints []string, key string) (subscriber.Subscriber, error) {
	return subscriber.NewEtcdSubscriber(subscriber.EtcdConf{
		Hosts: endpoints,
		Key:   key,
	})
}

// MustNewEtcdSubscriber creates an etcd subscriber, panics on error.
func MustNewEtcdSubscriber(endpoints []string, key string) subscriber.Subscriber {
	return subscriber.MustNewEtcdSubscriber(subscriber.EtcdConf{
		Hosts: endpoints,
		Key:   key,
	})
}

// NewConfigCenter creates a config center with an etcd subscriber.
func NewConfigCenter[T any](endpoints []string, key string, cfgType string) (configurator.Configurator[T], error) {
	ss, err := NewEtcdSubscriber(endpoints, key)
	if err != nil {
		return nil, err
	}
	return configurator.NewConfigCenter[T](configurator.Config{Type: cfgType}, ss)
}

// MustNewConfigCenter creates a config center with an etcd subscriber, panics on error.
func MustNewConfigCenter[T any](endpoints []string, key string, cfgType string) configurator.Configurator[T] {
	ss := MustNewEtcdSubscriber(endpoints, key)
	return configurator.MustNewConfigCenter[T](configurator.Config{Type: cfgType}, ss)
}

// ConfigKey returns the etcd config key for a service.
// Convention: aim-config-{service-name}
func ConfigKey(serviceName string) string {
	return "aim-config-" + serviceName
}

// InitConfigCenter initializes config center with an etcd subscriber.
// It loads remote config on startup (non-fatal if unavailable) and watches
// for hot-reload changes. The cfg parameter must be a pointer to a struct
// with json tags for MergeRemote to work correctly.
func InitConfigCenter(serviceName string, etcdHosts []string, cfg any) {
	if len(etcdHosts) == 0 {
		return
	}

	key := ConfigKey(serviceName)
	ss, err := NewEtcdSubscriber(etcdHosts, key)
	if err != nil {
		fmt.Printf("config center: cannot connect to etcd (%v), use local config only\n", err)
		return
	}

	// Load remote config on startup (non-fatal if unavailable)
	raw, err := ss.Value()
	if err == nil && raw != "" {
		if err := MergeRemote(cfg, []byte(raw)); err != nil {
			fmt.Printf("config center: merge remote config failed: %v\n", err)
		} else {
			fmt.Printf("config center: loaded remote config from etcd key=%s\n", key)
		}
	}

	// Watch for config changes and hot-reload
	if err := ss.AddListener(func() {
		raw, err := ss.Value()
		if err != nil || raw == "" {
			return
		}
		if err := MergeRemote(cfg, []byte(raw)); err != nil {
			fmt.Printf("config center: hot-reload merge failed: %v\n", err)
			return
		}
		fmt.Printf("config center: config hot-reloaded from etcd key=%s\n", key)
	}); err != nil {
		fmt.Printf("config center: add listener failed: %v\n", err)
	}
}
