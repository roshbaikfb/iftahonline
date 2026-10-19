package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes of the integrity violations the store layers map to
// domain errors.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
)

// IsUniqueViolation reports whether err (or an error it wraps) is a unique
// violation of constraint; an empty constraint matches any.
func IsUniqueViolation(err error, constraint string) bool {
	return violates(err, codeUniqueViolation, constraint)
}

// IsForeignKeyViolation reports whether err is a foreign-key violation of
// constraint; an empty constraint matches any.
func IsForeignKeyViolation(err error, constraint string) bool {
	return violates(err, codeForeignKeyViolation, constraint)
}

// IsCheckViolation reports whether err is a CHECK violation of
// constraint; an empty constraint matches any.
func IsCheckViolation(err error, constraint string) bool {
	return violates(err, codeCheckViolation, constraint)
}

func violates(err error, code, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == code && (constraint == "" || pgErr.ConstraintName == constraint)
}
