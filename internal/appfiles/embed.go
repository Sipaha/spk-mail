// Package appfiles embeds static application assets (icons).
package appfiles

import "embed"

//go:embed icons/*
var FS embed.FS
