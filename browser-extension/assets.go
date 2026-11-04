package browserextension

import "embed"

// SourceIdentity is part of version recovery. Change the local revision when
// changing the shipped fork, transport or compatibility contract.
const SourceIdentity = "dsh-work-extension/0.2.0/session-v2-r14;playwright=e8149b8257d32dcf8f72573ecc43e72439da7080;host=0.2.0;setup=1;wire=2"

// Files is the exact unpacked development module shipped with this host build.
//
//go:embed manifest.json icon-*.png *.js popup.html LICENSE NOTICE PROVENANCE.txt
var Files embed.FS
