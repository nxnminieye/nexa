package crudproto

import (
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/provenance"
)

func ParseLock(source provenance.DomainSource, data []byte) (Lock, error) {
	value, err := crudbuild.ParseLock(source, data)
	if err != nil {
		return Lock{}, wrapError(err)
	}
	return Lock{state: value}, nil
}

func CanonicalLockJSON(lock Lock) ([]byte, error) {
	if lock.state.APIVersion() != LockAPIVersion {
		return nil, newStateError("lock_digest_mismatch", "/lock")
	}
	return lock.state.CanonicalJSON(), nil
}

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	value, err := crudbuild.ParseSnapshot(source, data)
	if err != nil {
		return Snapshot{}, wrapError(err)
	}
	return Snapshot{state: value}, nil
}

func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state.Valid() == false {
		return nil, newStateError("document_state_invalid", "/snapshot")
	}
	return s.state.CanonicalJSON(), nil
}
