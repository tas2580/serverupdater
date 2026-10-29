package main

import (
	"flag"
	"fmt"
	"os"

	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"serverupdater/internal/branding"
	"serverupdater/internal/client"
	"serverupdater/internal/desktop"
)

var version = "dev"

func main() {
	profilesPath := flag.String("profiles", "", "Optionaler Pfad zu profiles.json")
	showVersion := flag.Bool("version", false, "Version anzeigen")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	a := app.NewWithID("de.serverupdater.desktop")
	a.SetIcon(branding.Icon)
	profiles, err := client.LoadProfiles(*profilesPath)
	if err != nil {
		w := a.NewWindow("ServerUpdater — Fehler")
		dialog.ShowError(err, w)
		w.ShowAndRun()
		os.Exit(1)
	}
	d := desktop.New(a, profiles, client.NewVault())
	d.Window.ShowAndRun()
}
