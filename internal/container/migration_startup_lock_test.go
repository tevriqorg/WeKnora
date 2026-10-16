package container

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRunStartupMigrationsSerializesPostgresAndPropagatesRunnerError(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer sqlDB.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 1))
	want := errors.New("migration failed")
	called := false
	err = runStartupMigrations(context.Background(), db, "postgres://fixture", database.MigrationOptions{}, func(dsn string, _ database.MigrationOptions) error {
		called = true
		require.Equal(t, "postgres://fixture", dsn)
		return want
	})
	require.True(t, called)
	require.ErrorIs(t, err, want)
	require.NoError(t, mock.ExpectationsWereMet())
}
