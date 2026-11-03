// Package modules registers every feature module with the app.
package modules

import (
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/modules/ddns"
	"github.com/icebays/al1add/internal/modules/ipdb"
	"github.com/icebays/al1add/internal/modules/ipfilter"
	"github.com/icebays/al1add/internal/modules/portforward"
	"github.com/icebays/al1add/internal/modules/secgroup"
	"github.com/icebays/al1add/internal/modules/ssl"
	"github.com/icebays/al1add/internal/modules/stun"
	"github.com/icebays/al1add/internal/modules/webservice"
	"github.com/icebays/al1add/internal/modules/wol"
)

// RegisterAll registers modules in start order: services first, then the
// features that depend on them.
func RegisterAll(app *core.App) {
	// shared services
	app.Register(ipdb.New(app))
	app.Register(ipfilter.New(app))
	app.Register(secgroup.New(app))
	app.Register(ssl.New(app))

	// network features
	app.Register(webservice.New(app))
	app.Register(portforward.New(app))
	app.Register(ddns.New(app))
	app.Register(stun.New(app))
	app.Register(wol.New(app))
}
