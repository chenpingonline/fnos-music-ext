package userdir

import (
	"bufio"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type User struct {
	Username string `json:"username"`
	UID      string `json:"uid"`
}

func ValidName(name string) bool {
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\\\x00:\r\n") {
		return false
	}
	return strings.TrimSpace(name) == name
}

// NAS-local account directory. No credentials or unofficial RPC session needed.
func List() ([]User, error) {
	f, e := os.Open("/etc/passwd")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return Parse(f)
}
func Parse(r io.Reader) ([]User, error) {
	out := []User{}
	s := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for s.Scan() {
		fields := strings.Split(s.Text(), ":")
		if len(fields) != 7 || !ValidName(fields[0]) {
			continue
		}
		uid, e := strconv.Atoi(fields[2])
		if e != nil || uid < 1000 || uid >= 60000 {
			continue
		}
		out = append(out, User{Username: fields[0], UID: strconv.Itoa(uid)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, s.Err()
}
func Lookup(name string) (User, bool) {
	if !ValidName(name) {
		return User{}, false
	}
	f, e := os.Open("/etc/passwd")
	if e != nil {
		return User{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) == 7 && fields[0] == name {
			uid, e := strconv.ParseUint(fields[2], 10, 32)
			if e == nil {
				return User{Username: name, UID: strconv.FormatUint(uid, 10)}, true
			}
		}
	}
	return User{}, false
}
