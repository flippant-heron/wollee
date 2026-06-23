package web

import "embed"

//go:embed index.html add-host.html settings.html static/*
var Assets embed.FS
