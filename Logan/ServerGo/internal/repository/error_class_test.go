package repository

import (
	"context"
	"errors"
	"fmt"
	driver "github.com/go-sql-driver/mysql"
	"strings"
	"testing"
)

func TestErrorClassSanitizesDatabaseDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "none"}, {context.DeadlineExceeded, "deadline"}, {context.Canceled, "cancelled"},
		{fmt.Errorf("wrapped: %w", &driver.MySQLError{Number: 1213, Message: "secret SQL and device"}), "mysql_1213"},
		{&driver.MySQLError{Number: 1205}, "mysql_1205"}, {&driver.MySQLError{Number: 1062}, "mysql_1062"}, {&driver.MySQLError{Number: 1064}, "mysql_1064"}, {&driver.MySQLError{Number: 9999, Message: "secret"}, "mysql_other"}, {errors.New("secret dsn"), "other"},
	} {
		got := ErrorClass(tc.err)
		if got != tc.want || strings.Contains(got, "secret") {
			t.Fatalf("unsafe or incorrect class: %q", got)
		}
	}
}
