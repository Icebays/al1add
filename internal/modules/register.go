// Package modules registers every feature module with the app.
package modules

import (
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/modules/ipdb"
	"github.com/icebays/al1add/internal/modules/ipfilter"
	"github.com/icebays/al1add/internal/modules/secgroup"
)

// RegisterAll registers modules in start order: services first, then the
// features that depend on them.
func RegisterAll(app *core.App) {
	app.Register(ipdb.New(app))
	app.Register(ipfilter.New(app))
	app.Register(secgroup.New(app))
}
