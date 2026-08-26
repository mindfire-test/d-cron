package dcron_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/mindfire-test/d-cron/dcron"
)

type dedicatedDriver struct{ openedDSN string }

func (d *dedicatedDriver) Open(dsn string) (driver.Conn, error) {
	d.openedDSN = dsn
	return nil, errNoRealConn
}

var errNoRealConn = context.Canceled

func TestWithDedicatedLockDriverOpensNamedDriver(t *testing.T) {
	const alias = "dcron-dedicated-alias"
	d := &dedicatedDriver{}
	sql.Register(alias, d)

	db, err := sql.Open(alias, "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, serr := dcron.New(
		db,
		dcron.WithDedicatedLockDriver(alias, "dsn-through-alias"),
		dcron.WithLogger(discardLogger()),
	)
	if serr == nil {
		t.Fatal("expected the fake driver's refusal to surface")
	}
	if d.openedDSN != "dsn-through-alias" {
		t.Fatalf("driver was opened with %q; want the option's DSN", d.openedDSN)
	}
	if !strings.Contains(serr.Error(), "dcron:") && !errors.Is(serr, errNoRealConn) {
		t.Fatalf("error should wrap the driver failure: %v", serr)
	}
}

func TestWithDedicatedLockSatisfiesSessionGate(t *testing.T) {
	const alias = "dcron-dedicated-gate"
	sql.Register(alias, &dedicatedDriver{})

	db, err := sql.Open(alias, "unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := dcron.New(db); err == nil {
		t.Fatal("New must refuse without session-stability wiring")
	} else if !isSessionStabilityErr(err) {
		t.Fatalf("want ErrSessionStabilityUnasserted family, got %v", err)
	}

	if _, err := dcron.New(
		db,
		dcron.WithDedicatedLockDriver(alias, "dsn"),
		dcron.WithLogger(discardLogger()),
	); err == nil || isSessionStabilityErr(err) {
		t.Fatalf("gate should pass with a dedicated lock conn; got %v", err)
	}
}

func isSessionStabilityErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "session stability not asserted")
}
