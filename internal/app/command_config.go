package app

import (
	"github.com/Aethersailor/m-ui/internal/config"
	"github.com/Aethersailor/m-ui/internal/store"
)

func initialSettings(cfg config.Config) store.InitialSettings {
	return store.InitialSettings{
		PanelTitle:         cfg.Panel.Title,
		UILanguage:         cfg.Panel.UILanguage,
		PublicHost:         cfg.Panel.PublicHost,
		PanelListenAddress: cfg.Server.ListenAddress,
		PanelListenPort:    cfg.Server.Port,
		TrustedProxyCIDRs:  []string{},
		MihomoBinaryPath:   cfg.Mihomo.BinaryPath,
		MihomoConfigDir:    cfg.Mihomo.ConfigDirectory,
		MihomoConfigPath:   cfg.Mihomo.ConfigPath,
		ControllerAddress:  cfg.Mihomo.ControllerAddress,
		BootstrapSecret:    cfg.Mihomo.ControllerSecret,
		MihomoServiceName:  cfg.Mihomo.ServiceName,
		HistoryLimit:       cfg.Mihomo.HistoryLimit,
	}
}
