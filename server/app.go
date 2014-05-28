package server

import (
	"github.com/siddontang/ledisdb/ledis"
	"net"
	"strings"
)

type App struct {
	cfg *Config

	listener net.Listener

	ldb *ledis.Ledis

	closed bool
}

func NewApp(cfg *Config) (*App, error) {
	app := new(App)

	app.closed = false

	app.cfg = cfg

	var err error

	if strings.Contains(cfg.Addr, "/") {
		app.listener, err = net.Listen("unix", cfg.Addr)
	} else {
		app.listener, err = net.Listen("tcp", cfg.Addr)
	}

	if err != nil {
		return nil, err
	}

	app.ldb, err = ledis.OpenWithConfig(&cfg.DB)
	if err != nil {
		return nil, err
	}

	return app, nil
}

func (app *App) Close() {
	if app.closed {
		return
	}

	app.listener.Close()

	app.ldb.Close()

	app.closed = true
}

func (app *App) Run() {
	for !app.closed {
		conn, err := app.listener.Accept()
		if err != nil {
			continue
		}

		newClient(conn, app.ldb)
	}
}