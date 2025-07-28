module khoj-provider

go 1.24.3

require fyne.io/systray v1.11.0

require (
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
)

replace github.com/getlantern/systray => fyne.io/systray v1.11.0
