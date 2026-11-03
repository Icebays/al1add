// Package modules registers every feature module with the app.
package modules

import (
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/modules/cloudflare"
	"github.com/icebays/al1add/internal/modules/cloudflared"
	"github.com/icebays/al1add/internal/modules/cron"
	"github.com/icebays/al1add/internal/modules/ddns"
	"github.com/icebays/al1add/internal/modules/docker"
	"github.com/icebays/al1add/internal/modules/frp"
	"github.com/icebays/al1add/internal/modules/ftp"
	"github.com/icebays/al1add/internal/modules/ipdb"
	"github.com/icebays/al1add/internal/modules/ipfilter"
	"github.com/icebays/al1add/internal/modules/portforward"
	"github.com/icebays/al1add/internal/modules/secgroup"
	"github.com/icebays/al1add/internal/modules/ssl"
	"github.com/icebays/al1add/internal/modules/storage"
	"github.com/icebays/al1add/internal/modules/stun"
	"github.com/icebays/al1add/internal/modules/waf"
	"github.com/icebays/al1add/internal/modules/webdav"
	"github.com/icebays/al1add/internal/modules/webservice"
	"github.com/icebays/al1add/internal/modules/webterminal"
	"github.com/icebays/al1add/internal/modules/wol"
)

// RegisterAll registers modules in start order: shared services first, then the
// features that depend on them.
func RegisterAll(app *core.App) {
	// shared services (other modules reference these)
	app.Register(ipdb.New(app))
	app.Register(ipfilter.New(app))
	app.Register(secgroup.New(app))
	app.Register(storage.New(app))
	app.Register(waf.New(app))
	app.Register(ssl.New(app))

	// network / proxy features
	app.Register(webservice.New(app))
	app.Register(portforward.New(app))
	app.Register(ddns.New(app))
	app.Register(stun.New(app))
	app.Register(wol.New(app))

	// tunnels
	app.Register(cloudflared.New(app))
	app.Register(frp.New(app))

	// file services
	app.Register(webdav.New(app))
	app.Register(ftp.New(app))

	// management / utilities
	app.Register(cloudflare.New(app))
	app.Register(docker.New(app))
	app.Register(cron.New(app))
	app.Register(webterminal.New(app))
}
