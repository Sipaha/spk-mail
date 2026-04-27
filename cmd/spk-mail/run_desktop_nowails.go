//go:build !wails

package main

import (
	"context"
	"fmt"
)

func runDesktop(_ context.Context) error {
	fmt.Println("desktop mode requires building with: go build -tags wails ./cmd/spk-mail")
	return nil
}
