//go:build integration

package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgresContainer(ctx context.Context) (dsn string, closer func(), err error) {
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_PASSWORD": "dcron-it",
			"POSTGRES_USER":     "dcron",
			"POSTGRES_DB":       "dcron_it",
		},
		AutoRemove: true,
		Cmd: []string{
			"-c", "tcp_keepalives_idle=30",
			"-c", "tcp_keepalives_interval=10",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").
			WithStartupTimeout(90 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return "", nil, fmt.Errorf("start postgres container: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		return "", nil, err
	}
	port, err := c.MappedPort(ctx, "5432")
	if err != nil {
		return "", nil, err
	}
	dsn = fmt.Sprintf("postgres://dcron:dcron-it@%s:%s/dcron_it?sslmode=disable", host, port.Port())
	return dsn, func() { _ = c.Terminate(context.Background()) }, nil
}
