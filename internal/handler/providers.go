package handler

import (
	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider"
	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider/commandcode"
	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider/ollamacloud"
	"github.com/dungsil/cpa-plugin-quota-plus/internal/provider/opencodego"
)

// providers is the single registration list. Adding a provider means adding
// its package and one line here.
var providers = []provider.Provider{
	opencodego.Provider{},
	ollamacloud.Provider{},
	commandcode.Provider{},
}
