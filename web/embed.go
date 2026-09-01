package web

import "embed"

//go:embed index.html admin.html assets/*
var Files embed.FS
