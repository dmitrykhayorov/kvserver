package storage

import "sync"

type KVStore struct {
	sync.RWMutex
	store map[string]string
}

func NewKVStore() *KVStore {
	return &KVStore{store: make(map[string]string)}
}

func (s *KVStore) Get(key string) (string, bool) {
	s.RLock()
	defer s.RUnlock()
	val, ok := s.store[key]
	return val, ok
}

func (s *KVStore) Put(key string, value string) {
	s.Lock()
	defer s.Unlock()
	s.store[key] = value
}

func (s *KVStore) Delete(key string) {
	s.Lock()
	defer s.Unlock()
	delete(s.store, key)
}
