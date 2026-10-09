package hostplugins

import "embed"

// pluginAssets are shipped with the application and installed into immutable,
// versioned directories for DSH's standard Host/Client module loader.
//
//go:embed shell/* account/* activity/*
var pluginAssets embed.FS
