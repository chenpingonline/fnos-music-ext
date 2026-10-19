package userdir

import (
	"strings"
	"testing"
)

func TestLocalUsersExcludeSystemAccounts(t *testing.T) {
	rows, err := Parse(strings.NewReader("root:x:0:0:root:/root:/bin/bash\nalice:x:1000:1000::/home/alice:/bin/bash\nbob:x:1001:1001::/home/bob:/sbin/nologin\nnobody:x:65534:65534::/:/bin/false\n"))
	if err != nil || len(rows) != 2 || rows[0].Username != "alice" || rows[1].UID != "1001" {
		t.Fatal(rows, err)
	}
}
