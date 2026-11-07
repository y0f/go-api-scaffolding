package testutil

import (
	"errors"
	"net/url"
)

// databaseURL selects the isolated database without copying a database override
// from the admin connection's query string. It never echoes credentials on error.
func databaseURL(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", errors.New("expected a postgres:// or postgresql:// URL with a host")
	}
	u.Path, u.RawPath = "/"+name, ""
	query := u.Query()
	query.Del("database")
	query.Del("dbname")
	u.RawQuery = query.Encode()
	return u.String(), nil
}
