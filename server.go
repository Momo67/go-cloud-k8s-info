package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	VERSION                = "0.3.2"
	APP                    = "go-info-server"
	DefaultPort            = 8080
	defaultServerIp        = "127.0.0.1"
	defaultServerPath      = "/"
	defaultSecondsToSleep  = 3
	secondsShutDownTimeout = 5 * time.Second // maximum number of second to wait before closing server
	defaultReadTimeout     = 2 * time.Minute // max time to read request from the client
	defaultWriteTimeout    = 2 * time.Minute // max time to write response to the client
	defaultIdleTimeout     = 2 * time.Minute // max time for connections using TCP Keep-Alive
	defaultNotFound        = "🤔 ℍ𝕞𝕞... 𝕤𝕠𝕣𝕣𝕪 :【𝟜𝟘𝟜 : ℙ𝕒𝕘𝕖 ℕ𝕠𝕥 𝔽𝕠𝕦𝕟𝕕】🕳️ 🔥"
	htmlHeaderStart        = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/skeleton/2.0.4/skeleton.min.css"/>`
	charsetUTF8            = "charset=UTF-8"
	MIMEAppJSON            = "application/json"
	MIMEAppJSONCharsetUTF8 = MIMEAppJSON + "; " + charsetUTF8
	HeaderContentType      = "Content-Type"
	httpErrMethodNotAllow  = "ERROR: Http method not allowed"
	initCallMsg            = "INITIAL CALL TO %s()\n"
)

type RuntimeInfo struct {
	Hostname     string              `json:"hostname"`      //  host name reported by the kernel.
	Pid          int                 `json:"pid"`           //  process id of the caller.
	PPid         int                 `json:"ppid"`          //  process id of the caller's parent.
	Uid          int                 `json:"uid"`           //  numeric user id of the caller.
	Appname      string              `json:"appname"`       // name of this application
	Version      string              `json:"version"`       // version of this application
	ParamName    string              `json:"param_name"`    // value of the name parameter (_NO_PARAMETER_NAME_ if name was not set)
	RemoteAddr   string              `json:"remote_addr"`   // remote client ip address
	GOOS         string              `json:"goos"`          // operating system
	GOARCH       string              `json:"goarch"`        // architecture
	Runtime      string              `json:"runtime"`       // go runtime at compilation time
	NumGoroutine string              `json:"num_goroutine"` // number of go routines
	NumCPU       string              `json:"num_cpu"`       // number of cpu
	Uptime       string              `json:"uptime"`        // tells how long this service was started
	EnvVars      []string            `json:"env_vars"`      // environment variables
	Headers      map[string][]string `json:"headers"`       // received headers
}

type ErrorConfig struct {
	err error
	msg string
}

//Error returns a string with an error and a specifics message
func (e *ErrorConfig) Error() string {
	return fmt.Sprintf("%s : %v", e.msg, e.err)
}

//GetPortFromEnv returns a valid TCP/IP listening ':PORT' string based on the values of environment variable :
//	PORT : int value between 1 and 65535 (the parameter defaultPort will be used if env is not defined)
//  in case the ENV variable PORT exists and contains an invalid integer the functions returns an empty string and an error
func GetPortFromEnv(defaultPort int) (string, error) {
	srvPort := defaultPort

	var err error
	val, exist := os.LookupEnv("PORT")
	if exist {
		srvPort, err = strconv.Atoi(val)
		if err != nil {
			return "", &ErrorConfig{
				err: err,
				msg: "ERROR: CONFIG ENV PORT should contain a valid integer.",
			}
		}
		if srvPort < 1 || srvPort > 65535 {
			return "", &ErrorConfig{
				err: err,
				msg: "ERROR: CONFIG ENV PORT should contain an integer between 1 and 65535",
			}
		}
	}
	return fmt.Sprintf(":%d", srvPort), nil
}

func getHtmlHeader(title string) string {
	return fmt.Sprintf("%s<title>%s</title></head>", htmlHeaderStart, title)
}

func getHtmlPage(title string) string {
	return getHtmlHeader(title) +
		fmt.Sprintf("\n<body><div class=\"container\"><h3>%s</h3></div></body></html>", title)
}

//waitForShutdownToExit will wait for interrupt signal SIGINT or SIGTERM and gracefully shutdown the server after secondsToWait seconds.
func waitForShutdownToExit(srv *http.Server, secondsToWait time.Duration) {
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	// Block until a signal is received.
	// wait for SIGINT (interrupt) 	: ctrl + C keypress, or in a shell : kill -SIGINT processId
	sig := <-interruptChan
	srv.ErrorLog.Printf("INFO: 'SIGINT %d interrupt signal received, about to shut down server after max %v seconds...'\n", sig, secondsToWait.Seconds())

	// create a deadline to wait for.
	ctx, cancel := context.WithTimeout(context.Background(), secondsToWait)
	defer cancel()
	// gracefully shuts down the server without interrupting any active connections
	// as long as the actives connections last less than shutDownTimeout
	// https://pkg.go.dev/net/http#Server.Shutdown
	if err := srv.Shutdown(ctx); err != nil {
		srv.ErrorLog.Printf("💥💥 ERROR: 'Problem doing Shutdown %v'\n", err)
	}
	<-ctx.Done()
	srv.ErrorLog.Println("INFO: 'Server gracefully stopped, will exit'")
	os.Exit(0)
}

//GoHttpServer is a struct type to store information related to all handlers of web server
type GoHttpServer struct {
	listenAddress string
	// later we will store here the connection to database
	//DB  *db.Conn
	logger     *log.Logger
	router     *http.ServeMux
	startTime  time.Time
	httpServer http.Server
}

//NewGoHttpServer is a constructor that initializes the server mux (routes) and all fields of the  GoHttpServer type
func NewGoHttpServer(listenAddress string, logger *log.Logger) *GoHttpServer {
	myServerMux := http.NewServeMux()
	myServer := GoHttpServer{
		listenAddress: listenAddress,
		logger:        logger,
		router:        myServerMux,
		startTime:     time.Now(),
		httpServer: http.Server{
			Addr:         listenAddress,       // configure the bind address
			Handler:      myServerMux,         // set the http mux
			ErrorLog:     logger,              // set the logger for the server
			ReadTimeout:  defaultReadTimeout,  // max time to read request from the client
			WriteTimeout: defaultWriteTimeout, // max time to write response to the client
			IdleTimeout:  defaultIdleTimeout,  // max time for connections using TCP Keep-Alive
		},
	}
	myServer.routes()

	return &myServer
}

// (*GoHttpServer) routes initializes all the handlers paths of this web server, it is called inside the NewGoHttpServer constructor
func (s *GoHttpServer) routes() {
	s.router.Handle("/", s.getMyDefaultHandler())
	s.router.Handle("/time", s.getTimeHandler())
	s.router.Handle("/wait", s.getWaitHandler(defaultSecondsToSleep))
	s.router.Handle("/readiness", s.getReadinessHandler())
	s.router.Handle("/health", s.getHealthHandler())

	//s.router.Handle("/hello", s.getHelloHandler())
}

// StartServer initializes all the handlers paths of this web server, it is called inside the NewGoHttpServer constructor
func (s *GoHttpServer) StartServer() {

	// Starting the web server in his own goroutine
	go func() {
		s.logger.Printf("INFO: Starting http server listening at http://localhost%s/", s.listenAddress)
		err := s.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			s.logger.Fatalf("💥💥 ERROR: 'Could not listen on %q: %s'\n", s.listenAddress, err)
		}
	}()
	s.logger.Printf("Server listening on : %s PID:[%d]", s.httpServer.Addr, os.Getpid())

	// Graceful Shutdown on SIGINT (interrupt)
	waitForShutdownToExit(&s.httpServer, secondsShutDownTimeout)

}

func (s *GoHttpServer) jsonResponse(w http.ResponseWriter, r *http.Request, result interface{}) {
	body, err := json.Marshal(result)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.Printf("ERROR: 'JSON marshal failed. Error: %v'", err)
		return
	}
	var prettyOutput bytes.Buffer
	json.Indent(&prettyOutput, body, "", "  ")
	w.Header().Set(HeaderContentType, MIMEAppJSONCharsetUTF8)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(prettyOutput.Bytes())
}

//############# BEGIN HANDLERS

func (s *GoHttpServer) getReadinessHandler() http.HandlerFunc {
	handlerName := "getReadinessHandler"
	s.logger.Printf(initCallMsg, handlerName)
	return func(w http.ResponseWriter, r *http.Request) {
		s.logger.Printf("TRACE: [%s] %s  path:'%s', RemoteAddrIP: [%s]\n", handlerName, r.Method, r.URL.Path, r.RemoteAddr)
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
func (s *GoHttpServer) getHealthHandler() http.HandlerFunc {
	handlerName := "getHealthHandler"
	s.logger.Printf(initCallMsg, handlerName)
	return func(w http.ResponseWriter, r *http.Request) {
		s.logger.Printf("TRACE: [%s] %s  path:'%s', RemoteAddrIP: [%s]\n", handlerName, r.Method, r.URL.Path, r.RemoteAddr)
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
func (s *GoHttpServer) getMyDefaultHandler() http.HandlerFunc {
	handlerName := "getMyDefaultHandler"

	s.logger.Printf(initCallMsg, handlerName)
	hostName, err := os.Hostname()
	if err != nil {
		s.logger.Printf("💥💥 ERROR: 'os.Hostname() returned an error : %v'", err)
		hostName = "#unknown#"
	}

	data := RuntimeInfo{
		Hostname:     hostName,
		Pid:          os.Getpid(),
		PPid:         os.Getppid(),
		Uid:          os.Getuid(),
		Appname:      APP,
		Version:      VERSION,
		ParamName:    "_NO_PARAMETER_NAME_",
		RemoteAddr:   "",
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		Runtime:      runtime.Version(),
		NumGoroutine: strconv.FormatInt(int64(runtime.NumGoroutine()), 10),
		NumCPU:       strconv.FormatInt(int64(runtime.NumCPU()), 10),
		Uptime:       fmt.Sprintf("%s", time.Since(s.startTime)),
		EnvVars:      os.Environ(),
		Headers:      map[string][]string{},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		remoteIp := r.RemoteAddr // ip address of the original request or the last proxy
		requestedUrlPath := r.URL.Path
		s.logger.Printf("TRACE: [%s] %s  path:'%s', RemoteAddrIP: [%s]\n", handlerName, r.Method, requestedUrlPath, remoteIp)
		switch r.Method {
		case http.MethodGet:
			if len(strings.TrimSpace(requestedUrlPath)) == 0 || requestedUrlPath == defaultServerPath {
				query := r.URL.Query()
				nameValue := query.Get("name")
				if nameValue != "" {
					data.ParamName = nameValue
				}
				data.RemoteAddr = remoteIp
				data.Headers = r.Header
				data.Uptime = ""
				s.jsonResponse(w, r, data)
				/*n, err := fmt.Fprintf(w, getHtmlPage(defaultMessage))
				if err != nil {
					s.logger.Printf("💥💥 ERROR: [%s] was unable to Fprintf. path:'%s', from IP: [%s], send_bytes:%d'\n", handlerName, requestedUrlPath, remoteIp, n)
					http.Error(w, "Internal server error. myDefaultHandler was unable to Fprintf", http.StatusInternalServerError)
					return
				}*/
				s.logger.Printf("SUCCESS: [%s] path:'%s', from IP: [%s]\n", handlerName, requestedUrlPath, remoteIp)
			} else {
				w.WriteHeader(http.StatusNotFound)
				n, err := fmt.Fprintf(w, getHtmlPage(defaultNotFound))
				if err != nil {
					s.logger.Printf("💥💥 ERROR: [%s] Not Found was unable to Fprintf. path:'%s', from IP: [%s], send_bytes:%d\n", handlerName, requestedUrlPath, remoteIp, n)
					http.Error(w, "Internal server error. myDefaultHandler was unable to Fprintf", http.StatusInternalServerError)
					return
				}
			}
		default:
			s.logger.Printf("%s. Request: %#v", httpErrMethodNotAllow, r)
			http.Error(w, httpErrMethodNotAllow, http.StatusMethodNotAllowed)
		}
	}
}
func (s *GoHttpServer) getTimeHandler() http.HandlerFunc {
	handlerName := "getTimeHandler"
	s.logger.Printf(initCallMsg, handlerName)
	return func(w http.ResponseWriter, r *http.Request) {
		s.logger.Printf("TRACE: [%s] %s  path:'%s', RemoteAddrIP: [%s]\n", handlerName, r.Method, r.URL.Path, r.RemoteAddr)
		if r.Method == http.MethodGet {
			now := time.Now()
			w.Header().Set(HeaderContentType, MIMEAppJSONCharsetUTF8)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "{\"time\":\"%s\"}", now.Format(time.RFC3339))
		} else {
			s.logger.Printf("%s. Request: %#v", httpErrMethodNotAllow, r)
			http.Error(w, httpErrMethodNotAllow, http.StatusMethodNotAllowed)
		}
	}
}
func (s *GoHttpServer) getWaitHandler(secondsToSleep int) http.HandlerFunc {
	handlerName := "getWaitHandler"
	s.logger.Printf(initCallMsg, handlerName)
	durationOfSleep := time.Duration(secondsToSleep) * time.Second
	return func(w http.ResponseWriter, r *http.Request) {
		s.logger.Printf("TRACE: [%s] %s  path:'%s', RemoteAddrIP: [%s]\n", handlerName, r.Method, r.URL.Path, r.RemoteAddr)
		if r.Method == http.MethodGet {
			w.Header().Set(HeaderContentType, MIMEAppJSONCharsetUTF8)
			time.Sleep(durationOfSleep) // simulate a delay to be ready
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "{\"waited\":\"%v seconds\"}", secondsToSleep)
		} else {
			s.logger.Printf("%s. Request: %#v", httpErrMethodNotAllow, r)
			http.Error(w, httpErrMethodNotAllow, http.StatusMethodNotAllowed)
		}
	}
}

//############# END HANDLERS
func main() {
	listenAddr, err := GetPortFromEnv(DefaultPort)
	if err != nil {
		log.Fatalf("💥💥 ERROR: 'calling GetPortFromEnv got error: %v'\n", err)
	}
	l := log.New(os.Stdout, fmt.Sprintf("HTTP_SERVER_%s ", APP), log.Ldate|log.Ltime|log.Lshortfile)
	l.Printf("INFO: 'Starting %s version:%s HTTP server on port %s'", APP, VERSION, listenAddr)
	server := NewGoHttpServer(listenAddr, l)
	server.StartServer()
}
