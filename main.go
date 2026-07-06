package main

import (
	"context"
	"crypto/tls"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/IceWhaleTech/CasaOS-Common/external"
	"github.com/IceWhaleTech/CasaOS-Common/model"
	"github.com/IceWhaleTech/CasaOS-Common/utils/constants"
	http2 "github.com/IceWhaleTech/CasaOS-Common/utils/http"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/coreos/go-systemd/daemon"

	"github.com/IceWhaleTech/CasaOS-Gateway/common"
	"github.com/IceWhaleTech/CasaOS-Gateway/route"
	"github.com/IceWhaleTech/CasaOS-Gateway/service"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const localhost = "127.0.0.1"

var (
	commit = "private build"
	date   = "private build"

	_state      *service.State
	_gateway    *http.Server
	_gatewayTLS *http.Server

	_managementServiceReady = make(chan struct{})
	_gatewayServiceReady    = make(chan struct{})

	ErrCheckURLNotOK = errors.New("check url did not return 200 OK")

	//go:embed build/sysroot/etc/casaos/gateway.ini.sample
	_confSample string
)

func init() {
	versionFlag := flag.Bool("v", false, "version")
	wwwPathFlag := flag.String("w", filepath.Join(constants.DefaultDataPath, "www"), "www path")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("v%s\n", common.Version)
		os.Exit(0)
	}

	println("git commit:", commit)
	println("build date:", date)

	_state = service.NewState()

	// create default config file if not exist
	ConfigFilePath := filepath.Join(constants.DefaultConfigPath, common.GatewayName+"."+common.GatewayConfigType)
	if _, err := os.Stat(ConfigFilePath); os.IsNotExist(err) {
		fmt.Println("config file not exist, create it")
		// create config file
		file, err := os.Create(ConfigFilePath)
		if err != nil {
			panic(err)
		}
		defer file.Close()

		// write default config
		_, err = file.WriteString(_confSample)
		if err != nil {
			panic(err)
		}
	}

	config, err := common.LoadConfig()
	if err != nil {
		panic(err)
	}

	logger.LogInit(
		config.GetString(common.ConfigKeyLogPath),
		config.GetString(common.ConfigKeyLogSaveName),
		config.GetString(common.ConfigKeyLogFileExt),
	)

	runtimePath := config.GetString(common.ConfigKeyRuntimePath)
	if err := _state.SetRuntimePath(runtimePath); err != nil {
		logger.Error("Failed to set runtime path", zap.Any("error", err), zap.Any(common.ConfigKeyRuntimePath, runtimePath))
		panic(err)
	}

	gatewayPort := config.GetString(common.ConfigKeyGatewayPort)
	if err := _state.SetGatewayPort(gatewayPort); err != nil {
		logger.Error("Failed to set gateway port", zap.Any("error", err), zap.Any(common.ConfigKeyGatewayPort, gatewayPort))
		panic(err)
	}

	gatewayTLS := service.TLSState{
		Enabled:  config.GetBool(common.ConfigKeyGatewayTLSEnabled),
		CertFile: config.GetString(common.ConfigKeyGatewayTLSCert),
		KeyFile:  config.GetString(common.ConfigKeyGatewayTLSKey),
		Domain:   config.GetString(common.ConfigKeyGatewayTLSDomain),
		Port:     config.GetString(common.ConfigKeyGatewayTLSPort),
	}
	if err := _state.SetGatewayTLS(gatewayTLS); err != nil {
		logger.Error("Failed to set gateway TLS state", zap.Any("error", err))
		panic(err)
	}

	if err := _state.SetWWWPath(*wwwPathFlag); err != nil {
		logger.Error("Failed to set www path", zap.Any("error", err), zap.String("wwwpath", *wwwPathFlag))
		panic(err)
	}

	if err := checkPrequisites(_state); err != nil {
		logger.Error("Failed to check prequisites", zap.Any("error", err))
		panic(err)
	}

	_state.OnGatewayPortChange(func(port string) error {
		config.Set(common.ConfigKeyGatewayPort, port)
		return config.WriteConfig()
	})

	_state.OnGatewayTLSChange(func(tls service.TLSState) error {
		config.Set(common.ConfigKeyGatewayTLSEnabled, tls.Enabled)
		config.Set(common.ConfigKeyGatewayTLSCert, tls.CertFile)
		config.Set(common.ConfigKeyGatewayTLSKey, tls.KeyFile)
		config.Set(common.ConfigKeyGatewayTLSDomain, tls.Domain)
		config.Set(common.ConfigKeyGatewayTLSPort, tls.Port)
		return config.WriteConfig()
	})
}

func main() {
	pidFilename, err := writePidFile(_state.GetRuntimePath())
	if err != nil {
		logger.Error("Failed to write pid file to runtime path", zap.Any("error", err), zap.Any("runtimePath", _state.GetRuntimePath()))
		panic(err)
	}

	defer cleanupFiles(
		_state.GetRuntimePath(),
		pidFilename, external.ManagementURLFilename, external.StaticURLFilename,
	)

	defer func() {
		if _gateway != nil {
			if err := _gateway.Shutdown(context.Background()); err != nil {
				logger.Error("Failed to stop gateway", zap.Any("error", err))
			}
		}
		if _gatewayTLS != nil {
			if err := _gatewayTLS.Shutdown(context.Background()); err != nil {
				logger.Error("Failed to stop HTTPS gateway", zap.Any("error", err))
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	kill := make(chan os.Signal, 1)
	signal.Notify(kill, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-kill
		cancel()
	}()

	go func() {
		<-_managementServiceReady
		<-_gatewayServiceReady

		if supported, err := daemon.SdNotify(false, daemon.SdNotifyReady); err != nil {
			logger.Error("Failed to notify systemd that gateway is ready", zap.Any("error", err))
		} else if supported {
			logger.Info("Notified systemd that gateway is ready")
		} else {
			logger.Info("This process is not running as a systemd service.")
		}
	}()

	app := fx.New(
		fx.Provide(func() *service.State { return _state }),
		fx.Provide(service.NewManagementService),
		fx.Provide(route.NewManagementRoute),
		fx.Provide(route.NewGatewayRoute),
		fx.Provide(route.NewStaticRoute),
		fx.Invoke(run),
	)

	if err := app.Start(ctx); err != nil {
		if err != context.Canceled {
			logger.Error("Failed to start gateway", zap.Any("error", err))
		}
	}
}

func run(
	lifecycle fx.Lifecycle,
	management *service.Management,
	managementRoute *route.ManagementRoute,
	gatewayRoute *route.GatewayRoute,
	staticRoute *route.StaticRoute,
) {
	// management server
	lifecycle.Append(
		fx.Hook{
			OnStart: func(context.Context) error {
				listener, err := net.Listen("tcp", net.JoinHostPort(localhost, "0"))
				if err != nil {
					return err
				}

				managementServer := &http.Server{
					Handler:           managementRoute.GetRoute(),
					ReadHeaderTimeout: 5 * time.Second,
				}

				urlFilePath, err := writeAddressFile(_state.GetRuntimePath(), external.ManagementURLFilename, "http://"+listener.Addr().String())
				if err != nil {
					return err
				}

				go func() {
					logger.Info("Management service is listening...",
						zap.Any("address", listener.Addr().String()),
						zap.Any("filepath", urlFilePath),
					)
					err := managementServer.Serve(listener)
					if err != nil {
						logger.Error("management server error", zap.Any("error", err))
						os.Exit(1)
					}
				}()

				if err := management.CreateRoute(&model.Route{
					Path:   "/v1/gateway/port",
					Target: "http://" + listener.Addr().String(),
				}); err != nil {
					return err
				}

				if err := management.CreateRoute(&model.Route{
					Path:   "/v1/gateway/https",
					Target: "http://" + listener.Addr().String(),
				}); err != nil {
					return err
				}

				_managementServiceReady <- struct{}{}

				return nil
			},
		},
	)

	// gateway service
	lifecycle.Append(
		fx.Hook{
			OnStart: func(ctx context.Context) error {
				route := gatewayRoute.GetRoute()

				if _state.GetGatewayPort() == "" {
					// check if a port is available starting from port 80/8080
					portsToCheck := []int{}
					for i := 80; i < 90; i++ {
						portsToCheck = append(portsToCheck, i)
					}

					for i := 8080; i < 8090; i++ {
						portsToCheck = append(portsToCheck, i)
					}

					port := ""
					for _, p := range portsToCheck {
						port = fmt.Sprintf("%d", p)
						logger.Info("Checking if port is available...", zap.Any("port", port))
						if listener, err := net.Listen("tcp", net.JoinHostPort("", port)); err == nil {
							if err = listener.Close(); err != nil {
								logger.Error("Failed to close listener", zap.Any("error", err), zap.Any("port", port))
								continue
							}
							break
						}
					}

					if port == "" {
						return errors.New("No port available for gateway to use")
					}

					if err := _state.SetGatewayPort(port); err != nil {
						return err
					}
				}

				_state.OnGatewayPortChange(func(port string) error {
					return reloadGateway(port, route)
				})

				if err := reloadGateway(_state.GetGatewayPort(), route); err != nil {
					return err
				}

				_state.OnGatewayTLSChange(func(tlsState service.TLSState) error {
					return reloadGatewayForTLS(tlsState, route)
				})

				if _state.GetGatewayTLS().Enabled {
					if err := reloadGatewayForTLS(_state.GetGatewayTLS(), route); err != nil {
						logger.Error("Failed to enable HTTPS on startup - falling back to HTTP only", zap.Any("error", err))
					}
				}

				_gatewayServiceReady <- struct{}{}

				return nil
			},
		})

	// static web
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, err := net.Listen("tcp", net.JoinHostPort(localhost, "0"))
			if err != nil {
				return err
			}

			staticServer := &http.Server{
				Handler:           staticRoute.GetRoute(),
				ReadHeaderTimeout: 5 * time.Second,
			}

			target := "http://" + listener.Addr().String()

			urlFilePath, err := writeAddressFile(_state.GetRuntimePath(), external.StaticURLFilename, target)
			if err != nil {
				return err
			}

			if err := management.CreateRoute(&model.Route{
				Path:   "/",
				Target: target,
			}); err != nil {
				return err
			}

			logger.Info(
				"Static web service is listening...",
				zap.Any("address", listener.Addr().String()),
				zap.Any("filepath", urlFilePath),
			)
			return staticServer.Serve(listener)
		},
	})
}

func reloadGateway(port string, route *http.ServeMux) error {
	listener, err := net.Listen("tcp", net.JoinHostPort("", port))
	if err != nil {
		return err
	}

	addr := listener.Addr().String()

	if _gateway != nil && _gateway.Addr == addr {
		logger.Info("Port is the same as current running gateway - no change is required")
		return nil
	}

	// start new gateway
	gatewayNew := &http.Server{
		Addr:              addr,
		Handler:           route,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		err := gatewayNew.Serve(listener)
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				logger.Info("A gateway is stopped", zap.Any("address", gatewayNew.Addr))
				return
			}
			logger.Error("Error when serving a gateway", zap.Any("error", err), zap.Any("address", gatewayNew.Addr))
		}
	}()

	// test if gateway is running
	url := "http://" + addr + "/ping"
	if err := checkURLWithRetry(url, 10); err != nil {
		return err
	}

	logger.Info("New gateway is listening...", zap.Any("address", gatewayNew.Addr))

	// stop old gateway
	if _gateway != nil {
		gatewayOld := _gateway
		go func() {
			logger.Info("Stopping previous gateway in 1 seconds...", zap.Any("address", gatewayOld.Addr))
			time.Sleep(time.Second) // so that any request to the old gateway gets a response
			if err := gatewayOld.Shutdown(context.Background()); err != nil {
				logger.Error("Error when stopping previous gateway", zap.Any("error", err), zap.Any("address", gatewayOld.Addr))
			}
		}()
	}

	_gateway = gatewayNew

	return nil
}

// reloadGatewayForTLS enables or disables HTTPS. When enabling, the app is served over TLS on
// tlsState.Port and the existing plain-HTTP gateway port is repurposed to redirect to HTTPS.
// When disabling, the HTTPS listener is torn down and the plain-HTTP gateway goes back to
// serving the app directly.
func reloadGatewayForTLS(tlsState service.TLSState, route *http.ServeMux) error {
	if !tlsState.Enabled {
		stopServer(&_gatewayTLS)
		scheduleForceSetGatewayHandler(_state.GetGatewayPort(), route)
		return nil
	}

	cert, err := tls.LoadX509KeyPair(tlsState.CertFile, tlsState.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	tlsPort := tlsState.Port
	if tlsPort == "" {
		tlsPort = "443"
	}

	tlsListener, err := tls.Listen("tcp", net.JoinHostPort("", tlsPort), &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("failed to listen on HTTPS port %s: %w", tlsPort, err)
	}

	gatewayNewTLS := &http.Server{
		Addr:              tlsListener.Addr().String(),
		Handler:           route,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := gatewayNewTLS.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Error when serving HTTPS gateway", zap.Any("error", err), zap.Any("address", gatewayNewTLS.Addr))
		}
	}()

	// the certificate is (or may be) self-signed, so this internal loopback health check
	// cannot rely on normal certificate validation.
	if err := checkTLSURLWithRetry("https://127.0.0.1:"+tlsPort+"/ping", 10); err != nil {
		_ = gatewayNewTLS.Close()
		return err
	}

	logger.Info("HTTPS gateway is listening...", zap.Any("address", gatewayNewTLS.Addr))

	stopServer(&_gatewayTLS)
	_gatewayTLS = gatewayNewTLS

	redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			return
		}

		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})

	// This request may itself be arriving through the very plain-HTTP gateway we're about to
	// tear down and rebuild on the same port (e.g. the initial "enable HTTPS" call always comes
	// in over plain HTTP, since HTTPS doesn't exist yet). Shutting that server down synchronously
	// from within its own in-flight request would deadlock, since Shutdown waits for active
	// connections - including this one - to finish. Doing the swap shortly after we return avoids
	// that self-wait.
	scheduleForceSetGatewayHandler(_state.GetGatewayPort(), redirectHandler)

	return nil
}

// scheduleForceSetGatewayHandler runs forceSetGatewayHandler shortly after returning, so it never
// blocks (or deadlocks on) the request that triggered it.
func scheduleForceSetGatewayHandler(port string, handler http.Handler) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := forceSetGatewayHandler(port, handler); err != nil {
			logger.Error("Failed to swap plain-HTTP gateway handler", zap.Any("error", err), zap.Any("port", port))
		}
	}()
}

// forceSetGatewayHandler replaces whatever is currently serving on `port` with `handler`, even if
// a server is already bound to that same address (unlike reloadGateway, which skips the swap in
// that case). This is needed to switch the plain-HTTP gateway between serving the app directly
// and redirecting to HTTPS, without changing its port. Since the address doesn't change, the old
// server has to be shut down *before* binding the new listener - reloadGateway's overlap-then-
// retire approach doesn't work when both listeners want the same port.
func forceSetGatewayHandler(port string, handler http.Handler) error {
	if _gateway != nil {
		if err := _gateway.Shutdown(context.Background()); err != nil {
			logger.Error("Error when stopping previous gateway", zap.Any("error", err), zap.Any("address", _gateway.Addr))
		}
		_gateway = nil
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("", port))
	if err != nil {
		return err
	}

	addr := listener.Addr().String()

	gatewayNew := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		err := gatewayNew.Serve(listener)
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				logger.Info("A gateway is stopped", zap.Any("address", gatewayNew.Addr))
				return
			}
			logger.Error("Error when serving a gateway", zap.Any("error", err), zap.Any("address", gatewayNew.Addr))
		}
	}()

	if err := checkURLWithRetry("http://"+addr+"/ping", 10); err != nil {
		return err
	}

	logger.Info("New gateway is listening...", zap.Any("address", gatewayNew.Addr))

	_gateway = gatewayNew

	return nil
}

// stopServer gracefully shuts down *server (if any) after a short delay and clears the pointer.
func stopServer(server **http.Server) {
	if *server == nil {
		return
	}

	old := *server
	*server = nil

	go func() {
		time.Sleep(time.Second) // so that any in-flight request gets a response
		if err := old.Shutdown(context.Background()); err != nil {
			logger.Error("Error when stopping server", zap.Any("error", err), zap.Any("address", old.Addr))
		}
	}()
}

// checkTLSURLWithRetry is like checkURLWithRetry but skips certificate verification, since it is
// only ever used to probe our own freshly generated/uploaded certificate on the loopback address.
func checkTLSURLWithRetry(url string, retry uint) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback self-check only
		},
	}

	count := retry
	var err error

	for count >= 0 {
		logger.Info("Checking if HTTPS service at URL is running...", zap.Any("url", url), zap.Any("retry", count))

		var resp *http.Response
		resp, err = client.Get(url)
		if err != nil {
			time.Sleep(time.Second)
			count--
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		err = ErrCheckURLNotOK
		time.Sleep(time.Second)
		count--
	}

	return err
}

func checkURLWithRetry(url string, retry uint) error {
	count := retry
	var err error

	for count >= 0 {
		logger.Info("Checking if service at URL is running...", zap.Any("url", url), zap.Any("retry", count))
		if err = checkURL(url); err != nil {
			time.Sleep(time.Second)
			count--
			continue
		}
		break
	}

	return err
}

func checkURL(url string) error {
	response, err := http2.Get(url, 5*time.Second)
	if err == nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusOK {
		return ErrCheckURLNotOK
	}

	return nil
}

func writePidFile(runtimePath string) (string, error) {
	filename := "gateway.pid"
	filepath := filepath.Join(runtimePath, filename)
	return filename, os.WriteFile(filepath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600)
}

func writeAddressFile(runtimePath string, filename string, address string) (string, error) {
	err := os.MkdirAll(runtimePath, 0o755)
	if err != nil {
		return "", err
	}

	filepath := filepath.Join(runtimePath, filename)
	return filepath, os.WriteFile(filepath, []byte(address), 0o600)
}

func cleanupFiles(runtimePath string, filenames ...string) {
	for _, filename := range filenames {
		err := os.Remove(filepath.Join(runtimePath, filename))
		if err != nil {
			logger.Error("Failed to cleanup file", zap.Any("error", err), zap.Any("filename", filename))
		}
	}
}

func checkPrequisites(state *service.State) error {
	path := state.GetRuntimePath()

	err := os.MkdirAll(path, 0o755)
	if err != nil {
		return fmt.Errorf("please ensure the owner of this service has write permission to that path %s", path)
	}

	return nil
}
