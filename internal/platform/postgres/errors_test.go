package postgres_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

func TestViolations(t *testing.T) {
	t.Parallel()
	wrap := func(code, constraint string) error {
		return fmt.Errorf("store: insert: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	tests := []struct {
		name string
		err  error
		fn   func(error, string) bool
		c    string
		want bool
	}{
		{"unique named", wrap("23505", "users_email_key"), postgres.IsUniqueViolation, "users_email_key", true},
		{"unique any", wrap("23505", "users_email_key"), postgres.IsUniqueViolation, "", true},
		{"unique other constraint", wrap("23505", "users_phone_key"), postgres.IsUniqueViolation, "users_email_key", false},
		{"unique wrong code", wrap("23503", "users_email_key"), postgres.IsUniqueViolation, "users_email_key", false},
		{"fk", wrap("23503", "x_fkey"), postgres.IsForeignKeyViolation, "x_fkey", true},
		{"check", wrap("23514", "bounds"), postgres.IsCheckViolation, "bounds", true},
		{"check wrong code", wrap("23505", "bounds"), postgres.IsCheckViolation, "bounds", false},
		{"not pg", errors.New("boom"), postgres.IsUniqueViolation, "", false},
		{"nil", nil, postgres.IsUniqueViolation, "", false},
	}
	for _, tt := range tests {
		if got := tt.fn(tt.err, tt.c); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
