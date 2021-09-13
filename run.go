package proxy

import (
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Run initializes the proxy andstarts the main listener loop.
// It blocks forever.
func Run() {
	if err := LoadConfig(); err != nil {
		log.Fatal(err)
	}

	if !Conf().NoPlugins {
		loadPlugins()
	}

	var err error
	switch Conf().AuthBackend {
	case "sqlite3":
		setAuthBackend(authSQLite3{})
	case "files":
		setAuthBackend(authFiles{})
	default:
		log.Fatal("invalid auth backend")
	}

	if !Conf().NoTelnet {
		go func() {
			if err := telnetServer(); err != nil {
				log.Fatal(err)
			}
		}()
	}

	addr, err := net.ResolveUDPAddr("udp", Conf().BindAddr)
	if err != nil {
		log.Fatal(err)
	}

	pc, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}

	l := listen(pc)
	defer l.Close()

	log.Println("listen", l.Addr())

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		<-sig

		close(telnetCh)

		if Conf().List.Enable {
			if err := announce(listRm); err != nil {
				log.Print(err)
			}
		}

		clts := l.clients()

		var wg sync.WaitGroup
		wg.Add(len(clts))

		for cc := range clts {
			go func(cc *ClientConn) {
				cc.Kick("Proxy shutting down.")
				wg.Done()
			}(cc)
		}

		wg.Wait()
		os.Exit(0)
	}()

	for {
		cc, err := l.accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				log.Print("stop listening")
				break
			}

			log.Print(err)
			continue
		}

		go func() {
			<-cc.Init()
			cc.Log("<->", "handshake completed")

			if len(Conf().Servers) == 0 {
				cc.Log("<-", "no servers")
				cc.Kick("No servers are configured.")
				return
			}

			addr, err := net.ResolveUDPAddr("udp", Conf().Servers[0].Addr)
			if err != nil {
				cc.Log("<-", "address resolution fail")
				cc.Kick("Server address resolution failed.")
				return
			}

			conn, err := net.DialUDP("udp", nil, addr)
			if err != nil {
				cc.Log("<-", "connection fail")
				cc.Kick("Server connection failed.")
				return
			}

			connect(conn, Conf().Servers[0].Name, cc)
		}()
	}

	select {}
}
