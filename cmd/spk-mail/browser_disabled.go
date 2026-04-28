//go:build desktop_only

package main

import (
	"context"
	"errors"
)

func runBrowser(_ context.Context, _ int, _ bool, _ string, _ bool) error {
	return errors.New("browser mode disabled in this build (desktop_only)")
}
