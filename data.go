package rwtxt

import (
	"embed"
)

var (
	//go:embed all:static
	_static embed.FS

	//go:embed templates/*
	_templates embed.FS
)
