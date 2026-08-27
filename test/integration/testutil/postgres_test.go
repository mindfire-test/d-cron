//go:build integration

package testutil_test

import (
	"testing"

	"github.com/mindfire-test/d-cron/test/integration/testutil"
)

func TestNewPostgres_HelperInterface(t *testing.T) {
	db, container := testutil.NewPostgres(t)
	if db == nil || container == nil {
		t.Log("Docker unavailable or skipped; testutil.NewPostgres clean skip verified")
		return
	}

	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 failed on primary db: %v", err)
	}

	conn2 := container.NewConnection(t)
	var two int
	if err := conn2.QueryRow("SELECT 2").Scan(&two); err != nil || two != 2 {
		t.Fatalf("SELECT 2 failed on second connection: %v", err)
	}
}
