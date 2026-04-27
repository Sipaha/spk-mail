// Package appfiles embeds static application assets (icons, default fixtures).
package appfiles

import "embed"

//go:embed icons/* fixtures/*
var FS embed.FS
