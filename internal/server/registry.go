package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type HostRecord struct {
	MAC      string    `json:"mac" yaml:"mac"`
	Hostname string    `json:"hostname" yaml:"hostname"`
	IP       string    `json:"ip" yaml:"ip"`
	LastSeen time.Time `json:"lastSeen" yaml:"lastSeen"`
}

type hostRegistryFile struct {
	Hosts []HostRecord `yaml:"hosts"`
}

type Registry struct {
	mu    sync.RWMutex
	path  string
	hosts map[string]HostRecord
}

func OpenRegistry(path string) (*Registry, error) {
	registry := &Registry{
		path:  path,
		hosts: make(map[string]HostRecord),
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return registry, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}

	var file hostRegistryFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("decode registry: %w", err)
	}

	for _, host := range file.Hosts {
		registry.hosts[strings.ToLower(host.MAC)] = host
	}

	return registry, nil
}

func (r *Registry) Upsert(host HostRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hosts[strings.ToLower(host.MAC)] = host
	return r.saveLocked()
}

func (r *Registry) FindByMAC(mac string) (HostRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	host, ok := r.hosts[strings.ToLower(mac)]
	return host, ok
}

func (r *Registry) FindByHostname(hostname string) (HostRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, host := range r.hosts {
		if strings.EqualFold(host.Hostname, hostname) {
			return host, true
		}
	}

	return HostRecord{}, false
}

func (r *Registry) List() []HostRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hosts := make([]HostRecord, 0, len(r.hosts))
	for _, host := range r.hosts {
		hosts = append(hosts, host)
	}

	sort.Slice(hosts, func(i, j int) bool {
		if !strings.EqualFold(hosts[i].Hostname, hosts[j].Hostname) {
			return strings.ToLower(hosts[i].Hostname) < strings.ToLower(hosts[j].Hostname)
		}
		return hosts[i].MAC < hosts[j].MAC
	})

	return hosts
}

func (r *Registry) saveLocked() error {
	hosts := make([]HostRecord, 0, len(r.hosts))
	for _, host := range r.hosts {
		hosts = append(hosts, host)
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].MAC < hosts[j].MAC
	})

	payload, err := yaml.Marshal(hostRegistryFile{Hosts: hosts})
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}

	tempPath := r.path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return fmt.Errorf("write registry temp file: %w", err)
	}

	if err := os.Rename(tempPath, r.path); err != nil {
		return fmt.Errorf("replace registry file: %w", err)
	}

	return nil
}
