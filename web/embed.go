package web

import "embed"

//go:embed index.html assets/*
var Files embed.FS
