package branding

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed server.png
var iconPNG []byte

// Icon is shared by the application and its windows on both desktop platforms.
var Icon = fyne.NewStaticResource("serverupdater.png", iconPNG)
