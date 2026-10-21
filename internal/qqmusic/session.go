package qqmusic

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (n *Native) load() error {
	info, err := os.Lstat(n.session)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return errors.New("insecure QQ Music session file")
	}
	raw, err := os.ReadFile(n.session)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &n.cred) != nil || !n.cred.valid() {
		return errors.New("invalid QQ Music session")
	}
	return nil
}
func (n *Native) persistLocked(c credential) error {
	if !c.valid() {
		return errors.New("QQ 音乐未返回完整登录凭证")
	}
	if n.session == "" {
		n.cred = c
		return nil
	}
	dir := filepath.Dir(n.session)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("insecure QQ Music session directory")
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	raw, _ := json.Marshal(c)
	if err = f.Chmod(0600); err == nil {
		var written int
		written, err = f.Write(raw)
		if err == nil && written != len(raw) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), n.session)
	}
	if err != nil {
		return err
	}
	n.cred = c
	return nil
}
func (n *Native) CommitLogin(directory, id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.session != "" || id != n.account || !n.cred.valid() {
		return errors.New("invalid QQ Music login draft")
	}
	target := filepath.Join(directory, "qqmusic-sessions", id+".json")
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("QQ Music session already exists or is inaccessible")
	}
	n.session = target
	if err := n.persistLocked(n.cred); err != nil {
		n.session = ""
		return err
	}
	return nil
}
func (n *Native) RollbackLogin() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.session != "" {
		_ = os.Remove(n.session)
		n.session = ""
	}
}
