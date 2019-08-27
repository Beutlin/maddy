package maddy

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/foxcpp/maddy/config"
	"github.com/foxcpp/maddy/log"
	"github.com/foxcpp/maddy/module"
)

type modInfo struct {
	instance module.Module
	cfg      config.Node
}

func Start(cfg []config.Node) error {
	instances := make(map[string]modInfo)
	globals := config.NewMap(nil, &config.Node{Children: cfg})
	globals.String("hostname", false, false, "", nil)
	globals.String("statedir", false, false, "", nil)
	globals.String("libexecdir", false, false, "", nil)
	globals.Custom("tls", false, false, nil, tlsDirective, nil)
	globals.Bool("auth_perdomain", false, nil)
	globals.StringList("auth_domains", false, false, nil, nil)
	globals.Custom("log", false, false, defaultLogOutput, logOutput, &log.DefaultLogger.Out)
	globals.Bool("debug", false, &log.DefaultLogger.Debug)
	globals.AllowUnknown()
	unmatched, err := globals.Process()
	if err != nil {
		return err
	}

	for _, block := range unmatched {
		var instName string
		var modAliases []string
		if len(block.Args) == 0 {
			instName = block.Name
		} else {
			instName = block.Args[0]
			modAliases = block.Args[1:]
		}

		modName := block.Name

		factory := module.Get(modName)
		if factory == nil {
			return config.NodeErr(&block, "unknown module: %s", modName)
		}

		if module.HasInstance(instName) {
			return config.NodeErr(&block, "config block named %s already exists", instName)
		}

		log.Debugln("module create", modName, instName)
		inst, err := factory(modName, instName, modAliases)
		if err != nil {
			return err
		}

		block := block
		module.RegisterInstance(inst, config.NewMap(globals.Values, &block))
		for _, alias := range modAliases {
			if module.HasInstance(alias) {
				return config.NodeErr(&block, "config block named %s already exists", alias)
			}
			module.RegisterAlias(alias, instName)
			log.Debugln("module alias", alias, "->", instName)
		}
		instances[instName] = modInfo{instance: inst, cfg: block}
	}

	for _, inst := range instances {
		if module.Initialized[inst.instance.InstanceName()] {
			log.Debugln("module init", inst.instance.Name(), inst.instance.InstanceName(), "skipped because it was lazily initialized before")
			continue
		}

		module.Initialized[inst.instance.InstanceName()] = true
		log.Debugln("module init", inst.instance.Name(), inst.instance.InstanceName())
		if err := inst.instance.Init(config.NewMap(globals.Values, &inst.cfg)); err != nil {
			return err
		}
	}

	sig := make(chan os.Signal, 5)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)

	s := <-sig
	log.Printf("signal received (%v), next signal will force immediate shutdown.", s)
	go func() {
		s := <-sig
		log.Printf("forced shutdown due to signal (%v)!", s)
		os.Exit(1)
	}()

	for _, inst := range instances {
		if closer, ok := inst.instance.(io.Closer); ok {
			log.Debugln("clean-up for module", inst.instance.Name(), inst.instance.InstanceName())
			if err := closer.Close(); err != nil {
				log.Printf("module %s (%s) close failed: %v", inst.instance.Name(), inst.instance.InstanceName(), err)
			}
		}
	}

	return nil
}
