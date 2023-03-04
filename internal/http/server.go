package http

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	jwt "github.com/golang-jwt/jwt"
	"github.com/sorenisanerd/gotty/backend/localcommand"
	"github.com/sorenisanerd/gotty/server"
)

func renderResponse(header http.Header, hostName string, adminName string, adminEmail string) string {

	return fmt.Sprintf(`
 	<!DOCTYPE html>
	<head>
		<title>Welcome to Border0</title>
		<style>
			body {
				background-color: #2D2D2D;
			}
			
			h1 {
				color: #C26356;
				font-size: 30px;
				font-family: Menlo, Monaco, fixed-width;
			}
			
			p {
				color: white;
				font-family: "Source Code Pro", Menlo, Monaco, fixed-width;
			}
			a {
				color: white;
				font-family: "Source Code Pro", Menlo, Monaco, fixed-width;
			  }
		</style>
	</head>
	<body>
		<h1>🚀 Welcome to the Border0 built-in webserver</h1>
		
		<p>Hi and welcome %s (%s)!<br><br>
		You're visiting the built-in Border0 webserver. This is the administrator of the Border0 Organization that started this web service: <br><i><u>%s (%s)</u></i> <br><br>

		You can now start to make your own web, ssh, or database applications available through Border0. <br><br>
		Check out the documentation for more information: <a href='https://docs.border0.com'>https://docs.border0.com</a></p>
		</p>
		<p> <br><br>
		
		Have a great day! 😊 🚀 <br><br>
		(you're visiting %s from IP %s) 
		</p>
	</body>
	</html>
	`, header["X-Auth-Name"][0], header["X-Auth-Email"][0], adminName, adminEmail, hostName, header["X-Real-Ip"][0])

}
func StartLocalHTTPServer(dir string, l net.Listener) error {
	mux := http.NewServeMux()

	if dir == "" {
		// Get Org admin info
		adminName := "Unknown"
		adminEmail := "Unknown"

		admindata, err := getAdminData()
		if err != nil {
			fmt.Println("Warning: Could not get admin data: name", err)
		} else {
			if _email, ok := admindata["user_email"].(string); ok {
				adminEmail = _email

			} else {
				fmt.Println("Warning: Could not get admin data: email")

			}
			if _name, ok := admindata["name"].(string); ok {
				adminName = _name
			} else {
				fmt.Println("Warning: Could not get admin data: name")
			}

		}

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, renderResponse(r.Header, r.Host, adminName, adminEmail))
		})

	} else {
		fs := http.FileServer(http.Dir(dir))
		mux.Handle("/", http.StripPrefix("/", fs))
	}

	err := http.Serve(l, mux)
	if err != nil {
		return err
	}

	return nil
}

func getAdminData() (jwt.MapClaims, error) {
	admintoken, err := GetToken()
	if err != nil {
		return nil, err
	}
	token, err := jwt.Parse(admintoken, nil)
	if token == nil {
		return nil, err
	}

	claims, _ := token.Claims.(jwt.MapClaims)
	return claims, nil
}

func StartGoTtyServer(l net.Listener, port int, webttyString string) error {
	backendOptions := &localcommand.Options{}

	ttySrvOptions := &server.Options{
		EnableTLS:       false,
		EnableBasicAuth: false,
		EnableReconnect: true,
		Address:         "127.0.0.1",
		Port:            fmt.Sprintf("%d", port),
		TitleFormat:     "Border0 Shell",
		PermitWrite:     true,
	}
	// spit out the command
	commandsMap := strings.Split(webttyString, " ")
	factory, _ := localcommand.NewFactory(commandsMap[0], commandsMap[1:], backendOptions)

	srv, err := server.New(factory, ttySrvOptions)
	if err != nil {
		exit(err, 3)
	}

	ctx, cancel := context.WithCancel(context.Background())
	gCtx, gCancel := context.WithCancel(context.Background())

	/*
		TODO: This is a hack to get the port number of the gotty server
		ideally we use the listener we created above, but the gotty server
		does not support that yet. So we have to do this hack to get the port
		maybe just re-implenet this and pass the listner to the gotty server
		https://github.com/sorenisanerd/gotty/blob/v1.5.0/server/server.go#L102
	*/

	errs := make(chan error, 1)
	go func() {
		// Now we should start the server, but we want to give it the Listner we created
		// so we can use the same port for both the webserver and the gotty server
		errs <- srv.Run(ctx, server.WithGracefullContext(gCtx))
	}()
	err = waitSignals(errs, cancel, gCancel)

	if err != nil && err != context.Canceled {
		fmt.Printf("Error: %s\n", err)
		exit(err, 8)
	}

	return nil
}

func exit(err error, code int) {
	if err != nil {
		fmt.Println(err)
	}
	os.Exit(code)
}

func waitSignals(errs chan error, cancel context.CancelFunc, gracefullCancel context.CancelFunc) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(
		sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	select {
	case err := <-errs:
		return err

	case s := <-sigChan:
		switch s {
		case syscall.SIGINT:
			gracefullCancel()
			fmt.Println("C-C to force close")
			select {
			case err := <-errs:
				return err
			case <-sigChan:
				fmt.Println("Force closing...")
				cancel()
				return <-errs
			}
		default:
			cancel()
			return <-errs
		}
	}
}

func FindFreePort() (int, error) {
	rand.Seed(time.Now().UnixNano())
	minPort := 10000
	maxPort := 50000
	for i := 0; i < 1000; i++ {
		port := rand.Intn(maxPort-minPort) + minPort
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			l.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("Could not find a free TCP port between %d and %d", minPort, maxPort)

}
