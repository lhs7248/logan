package repository

import (
	"context"
	"errors"
	driver "github.com/go-sql-driver/mysql"
)

// ErrorClass exposes only bounded classes, never driver messages, SQL or inputs.
func ErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var dbError *driver.MySQLError
	if errors.As(err, &dbError) {
		switch dbError.Number {
		case 1213:
			return "mysql_1213"
		case 1205:
			return "mysql_1205"
		case 1062:
			return "mysql_1062"
		case 1064:
			return "mysql_1064"
		default:
			return "mysql_other"
		}
	}
	return "other"
}
