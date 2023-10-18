/*
Copyright © 2020 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARR    ANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/borderzero/border0-cli/cmd/logger"
	"github.com/borderzero/border0-cli/internal/api"
	"github.com/borderzero/border0-cli/internal/api/models"
	"github.com/borderzero/border0-cli/internal/border0"
	"github.com/borderzero/border0-cli/internal/http"
	"github.com/borderzero/border0-cli/internal/ssh/server"
	"github.com/prometheus/procfs"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"

	backoff "github.com/cenkalti/backoff/v4"
	pprocess "github.com/shirou/gopsutil/v3/process"
	"github.com/spf13/cobra"
)

// versionCmd represents the version command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run command",

	Run: func(cmd *cobra.Command, args []string) {
		// Start a process
		var processPath string
		var processArgs []string

		if len(args) == 0 && runcommand == "" {
			cmd.Help()
			return
		}

		if runcommand == "" {
			processPath = args[0]
			processArgs = args[1:]

		} else {
			args = strings.Split(runcommand, " ")
			processPath = args[0]
			processArgs = args[1:]
		}

		if runtime.GOOS != "linux" {
			log.Println("Warning: the run command is meant to Run containers processes and may not work well on non-Linux enviroments")
		}

		process := exec.Command(processPath, processArgs...)
		process.Stdout = cmd.OutOrStdout()
		process.Stderr = cmd.OutOrStderr()
		process.Stdin = cmd.InOrStdin()
		err := process.Start()
		if err != nil {
			log.Printf("cmd.Start: %v", err)
			os.Exit(1)
		}

		// Let's make sure we catch the signals
		cleanupDone := make(chan bool)
		quitChannelSsh := make(chan bool)
		quitChannelHttp := make(chan bool)
		chsyscall := make(chan os.Signal)

		handleSignals(chsyscall)

		// This makes sure we intercept the signals and relay them to the process
		go func() {
			for {
				syscall := <-chsyscall
				//fmt.Printf("Received an interrupt, signalling process...+%v", syscall)
				process.Process.Signal(syscall)
			}

		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		border0API := api.NewAPI(api.WithVersion(version))
		border0API.StartRefreshAccessTokenJob(ctx)

		// HTTP go routines
		go processStats(*process)
		go createHTTPSocketStartTunnel(ctx, border0API, quitChannelHttp, cleanupDone)

		// SSH go routines
		go createSocketStartTunnel(ctx, border0API, quitChannelSsh, cleanupDone)

		process.Wait() // will wait until finished

		// if done, then cleanup.. this makes sure we don't leave any sockets orphans
		quitChannelSsh <- true
		quitChannelHttp <- true

		// wait for cleanup to finish
		// We'll set a ticker as well, to make sure we have a timeout
		// In case the cleanup doesn't finish or take to long
		ticker := time.NewTicker(2000 * time.Millisecond)
		for {
			select {
			case <-ticker.C:
				fmt.Println("Timeout reached, exiting")
				os.Exit(process.ProcessState.ExitCode())
			case <-cleanupDone:
				//fmt.Println("Process finished with exit code", process.ProcessState.ExitCode())
				// TODO need to make sure, both ssh and http are done
				time.Sleep(100 * time.Millisecond)
				os.Exit(process.ProcessState.ExitCode())
			}
		}

	},
}

func createSocketStartTunnel(ctx context.Context, border0API *api.Border0API, quitChannelSsh chan bool, cleanupDone chan bool) {
	socketId := ""

	go func() {
		// This will make sure we cleanup the socket as soon as we get something on the quitChannel
		<-quitChannelSsh
		if socketId != "" {
			if err := border0API.DeleteSocket(ctx, socketId); err != nil {
				log.Fatalf("failed to cleunup socket: %s", err)
			}
		}
		// And let our parent know we're done with the clean up
		cleanupDone <- true

	}()

	for {
		b := backoff.NewExponentialBackOff()
		b.InitialInterval = 2 * time.Second
		b.MaxInterval = 60 * time.Second
		retriesTentimesWithBackoff := backoff.WithMaxRetries(b, 10)

		notify := func(err error, t time.Duration) {
			log.Printf("%v Will try again in: %s", err, t.Round(time.Second))
		}

		_ = backoff.RetryNotify(func() error {
			hostname, err := os.Hostname()
			if err != nil {
				hostname = "unknown-container"
			}

			socketToCreate := &models.Socket{
				Name:                           "ssh-" + hostname,
				Description:                    "border0 systems stats " + hostname,
				SocketType:                     "ssh",
				CloudAuthEnabled:               true,
				ConnectorAuthenticationEnabled: connectorAuthEnabled,
			}

			socketFromAPI, err := border0API.CreateSocket(ctx, socketToCreate)
			if err != nil {
				return fmt.Errorf("failed to create socket: %s", err)
			}

			socketId = socketFromAPI.SocketID

			policies, err := border0API.GetPoliciesBySocketID(socketFromAPI.SocketID)
			if err != nil {
				log.Fatalf("failed to get policies: %s", err)
			}

			fmt.Print(print_socket(*socketFromAPI, policies))
			SetRlimit()

			socket, err := border0.NewSocket(ctx, border0API, socketFromAPI.SocketID, logger.Logger)
			if err != nil {
				return fmt.Errorf("failed to create socket: %s", err)
			}

			socket.WithVersion(version)

			if proxyHost != "" {
				if err := socket.WithProxy(proxyHost); err != nil {
					log.Fatalf("error: %v", err)
				}
			}

			l, err := socket.Listen()
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			defer l.Close()

			sshServer, err := server.NewServer(logger.Logger, socket.Organization.Certificates["ssh_public_key"])
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			err = sshServer.Serve(l)

			return err
		}, retriesTentimesWithBackoff, notify)

	}

}

func createHTTPSocketStartTunnel(ctx context.Context, border0API *api.Border0API, quitChannelHttp chan bool, cleanupDone chan bool) {
	socketId := ""

	go func() {
		// This will make sure we cleanup the socket as soon as we get something on the quitChannel
		<-quitChannelHttp
		if socketId != "" {
			if err := border0API.DeleteSocket(ctx, socketId); err != nil {
				log.Fatalf("failed to cleunup socket: %s", err)
			}
		}
		// And let our parent know we're done with the clean up
		cleanupDone <- true

	}()
	// Start a goroutine to wait for the process to exit

	for {

		b := backoff.NewExponentialBackOff()
		b.InitialInterval = 2 * time.Second
		b.MaxInterval = 60 * time.Second
		retriesTentimesWithBackoff := backoff.WithMaxRetries(b, 10)

		notify := func(err error, t time.Duration) {
			log.Printf("%v Will try again in: %s", err, t.Round(time.Second))
		}

		_ = backoff.RetryNotify(func() error {
			hostname, err := os.Hostname()
			if err != nil {
				hostname = "unknown-container"
			}
			socketToCreate := &models.Socket{
				Name:                           "status-" + hostname,
				Description:                    "border0 systems stats " + hostname,
				SocketType:                     "http",
				CloudAuthEnabled:               true,
				ConnectorAuthenticationEnabled: connectorAuthEnabled,
			}

			socketFromAPI, err := border0API.CreateSocket(ctx, socketToCreate)
			if err != nil {
				return fmt.Errorf("failed to create socket: %s", err)
			}

			socketId = socketFromAPI.SocketID

			policies, err := border0API.GetPoliciesBySocketID(socketFromAPI.SocketID)
			if err != nil {
				log.Fatalf("failed to get policies: %s", err)
			}

			fmt.Print(print_socket(*socketFromAPI, policies))

			SetRlimit()

			socket, err := border0.NewSocket(ctx, border0API, socketFromAPI.SocketID, logger.Logger)
			if err != nil {
				return fmt.Errorf("failed to create socket: %s", err)
			}

			socket.WithVersion(version)

			if proxyHost != "" {
				if err := socket.WithProxy(proxyHost); err != nil {
					log.Fatalf("error: %v", err)
				}
			}

			l, err := socket.Listen()
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			defer l.Close()

			http.StartLocalHTTPServer("/tmp/border0stats", l)

			return err
		}, retriesTentimesWithBackoff, notify)
	}

}

func processStats(process exec.Cmd) {
	os.MkdirAll("/tmp/border0stats", os.ModePerm)

	for {

		f, err := os.OpenFile("/tmp/border0stats/index.html", os.O_WRONLY|os.O_CREATE, 0600)

		if err != nil {
			log.Printf("Error: %v", err)
			return
		}
		defer f.Close()
		w := bufio.NewWriter(f)
		time.Sleep(2 * time.Second)

		fs, err := procfs.NewFS("/proc")
		if err != nil {
			log.Printf("failed to open procfs: %v", err)
		}

		p, err := fs.Proc(process.Process.Pid)
		if err != nil {
			log.Printf("could not get process1: %s", err)
		}

		stat, err := p.Stat()

		if err != nil {
			log.Printf("could not get process stat: %s", err)
		}

		loadavg, err := fs.LoadAvg()
		if err != nil {
			log.Printf("failed to get loadavg: %v", err)
		}

		//fmt.Printf("Process %d has %d open file descriptors
		//fmt.Printf("Process %d: %
		exec, _ := p.CmdLine()
		//netstat, _ := p.Netstat()

		fmt.Fprintf(w, `
		<!DOCTYPE html>
	<head>
		<title>Welcome to Border0</title>

		<style>

			body {
				background-color: #2D2D2D;
				color: white;
				font-family: "Source Code Pro", Menlo, Monaco, fixed-width;
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


		<h1>🚀 Border0 Process data</h1>


		`)

		//fmt.Fprintf(w, "command:  %s<br>", stat.Comm)
		fmt.Fprintf(w, "command:  %s<br>", strings.Join(exec, ""))

		fmt.Fprintf(w, "<hr>")
		fmt.Fprintf(w, "cpu time: %fs<br>", stat.CPUTime())
		fmt.Fprintf(w, "vsize:    %dB<br>", stat.VirtualMemory())
		fmt.Fprintf(w, "rss:      %dB<br>", stat.ResidentMemory())
		fmt.Fprintf(w, "<hr>")
		if loadavg != nil {
			fmt.Fprintf(w, "Load last 1min:     %.2f<br>", loadavg.Load1)
			fmt.Fprintf(w, "Load last 5min:     %.2f<br>", loadavg.Load5)
			fmt.Fprintf(w, "Load last 15min:    %.2f<br>", loadavg.Load15)
			fmt.Fprintf(w, "<hr>")
		}
		//fmt.Fprintf(w, "<h1>Netstat</h1><br>")
		//fmt.Fprintf(w, "netstat:     %+v<br>", netstat)

		fmt.Fprintf(w, "====processes====<br>")
		pps, _ := pprocess.Processes()
		for pid := range pps {
			p := pps[pid]
			ret, _ := pprocess.NewProcess(int32(p.Pid))
			g, _ := ret.Cmdline()
			fmt.Fprintf(w, "%d => %s<br>", p.Pid, g)

		}

		fmt.Fprintf(w, "<hr>")
		fmt.Fprintf(w, "<h1>Host info</h1>")
		hi, _ := host.Info()

		fmt.Fprintf(w, "hostname:     %s<br>", hi.Hostname)
		fmt.Fprintf(w, "uptime:       %d<br>", hi.Uptime)
		t := time.Unix(int64(hi.BootTime), 0)
		fmt.Fprintf(w, "bootTime:     %s<br>", t)
		fmt.Fprintf(w, "procs:        %d<br>", hi.Procs)
		fmt.Fprintf(w, "os:           %s<br>", hi.OS)
		fmt.Fprintf(w, "platform:     %s<br>", hi.Platform)
		fmt.Fprintf(w, "platformFamily: %s<br>", hi.PlatformFamily)
		fmt.Fprintf(w, "platformVersion: %s<br>", hi.PlatformVersion)
		fmt.Fprintf(w, "kernelVersion: %s<br>", hi.KernelVersion)
		fmt.Fprintf(w, "virtualizationSystem: %s<br>", hi.VirtualizationSystem)
		fmt.Fprintf(w, "virtualizationRole: %s<br>", hi.VirtualizationRole)
		fmt.Fprintf(w, "hostid: %s<br>", hi.HostID)

		fmt.Fprintf(w, "<hr>")

		v, _ := mem.VirtualMemory()

		fmt.Fprintf(w, "====Virtual Memory====<br>")
		// almost every return value is a struct
		fmt.Fprintf(w, "Total: %v<br> Free:%v<br> UsedPercent:%f%%<br><hr>", v.Total, v.Free, v.UsedPercent)

		fmt.Fprintf(w, "====Swap data====<br>")
		sm, _ := mem.SwapMemory()
		//fmt.Fprintf(w, "%v swap memory", sm)
		fmt.Fprintf(w, "Total: %v<br> Free:%v<br> UsedPercent:%f%%<br><hr>", sm.Total, sm.Free, sm.UsedPercent)

		fmt.Fprintf(w, "====CPU data====<br>")
		cput, _ := cpu.Times(true)
		for _, cpu := range cput {
			fmt.Fprintf(w, "CPU: %s<br> user:%f<br> system:%f<br> idle:%f<br> nice:%f<br> iowait:%f<br> irq:%f<br> softirq:%f<br> steal:%f<br> guest:%f<br> guestNice:%f<br><hr>", cpu.CPU, cpu.User, cpu.System, cpu.Idle, cpu.Nice, cpu.Iowait, cpu.Irq, cpu.Softirq, cpu.Steal, cpu.Guest, cpu.GuestNice)
		}
		//fmt.Fprintf(w, "%+v<br>", cput)

		fmt.Fprintf(w, "====CPU Info====<br>")
		cpui, _ := cpu.Info()
		for _, cpu := range cpui {
			fmt.Fprintf(w, "CPU: %d<br> cores:%d<br> mhz:%f<br> cacheSize:%d<br> modelname:%s<br> vendorId:%s<br> physicalId:%s<br> cpuFamily:%s<br> model:%s<br> stepping:%d<br> flags:%s<br><hr>", cpu.CPU, cpu.Cores, cpu.Mhz, cpu.CacheSize, cpu.ModelName, cpu.VendorID, cpu.PhysicalID, cpu.Family, cpu.Model, cpu.Stepping, cpu.Flags)
		}
		//fmt.Fprintf(w, "%+v<br>", cpui)

		fmt.Fprintf(w, "====Disk usage /====<br>")
		disku, _ := disk.Usage("/")
		fmt.Fprintf(w, "Total: %v<br> Free:%v<br> UsedPercent:%0.2f%%<br><hr>", disku.Total, disku.Free, disku.UsedPercent)

		fmt.Fprintf(w, "====Network interfaces====<br>")
		ni, _ := net.Interfaces()
		for _, n := range ni {
			fmt.Fprintf(w, "Name: %s<br> HardwareAddr:%s<br> Flags:%s<br> MTU:%d<br> Index:%d<br> Addrs:%s<br>", n.Name, n.HardwareAddr, n.Flags, n.MTU, n.Index, n.Addrs)
		}
		fmt.Fprintf(w, "<hr>")
		//fmt.Fprintf(w, "%+v<br>", ni)

		fmt.Fprintf(w, "====Net io counter====<br>")
		nioc, _ := net.IOCounters(true)
		//fmt.Fprintf(w, "%+v<br>", nioc)
		for _, n := range nioc {
			fmt.Fprintf(w, "Name: %s<br> BytesSent:%d<br> BytesRecv:%d<br> PacketsSent:%d<br> PacketsRecv:%d<br> Errin:%d<br> Errout:%d<br> Dropin:%d<br> Dropout:%d<br><br>", n.Name, n.BytesSent, n.BytesRecv, n.PacketsSent, n.PacketsRecv, n.Errin, n.Errout, n.Dropin, n.Dropout)
		}
		fmt.Fprintf(w, "<hr>")

		fmt.Fprintf(w, "====net protocl counters====<br>")
		npc, _ := net.ProtoCounters([]string{})
		//fmt.Fprintf(w, "%+v<br>", npc)
		for _, n := range npc {
			fmt.Fprintf(w, "Protocol: %s<br>", n.Protocol)
			fmt.Fprintf(w, "%+v", n)
			fmt.Fprintf(w, "<br>")

		}

		fmt.Fprintf(w, "</body></html>")

		w.Flush()

		f.Close()
		time.Sleep(5 * time.Second)

	}

}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&runcommand, "command", "c", "", "Command to execute")
}
