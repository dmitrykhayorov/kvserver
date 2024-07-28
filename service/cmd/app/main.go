package main

import (
	"flag"
	"kvstorage/service"
	"log"
	"os"
	"os/signal"
)

var (
	replicationFactor int
	addr              string
	nodeId            string
	joinAddr          string
)

func initOptions() {
	flag.IntVar(&replicationFactor, "rfactor", 2, "Amount of replicas")
	flag.StringVar(&addr, "addr", "localhost:8080", "Address for service")
	flag.StringVar(&nodeId, "nid", addr, "node id. if not set same as addr")
	flag.StringVar(&joinAddr, "join", "", "Set join address, if any")
}

func main() {
	initOptions()
	flag.Parse()

	serv := service.NewService(nodeId, addr, replicationFactor)
	service.DPrintf("created new service: %s", serv.Addr)

	if err := serv.Start(joinAddr); err != nil {
		log.Fatalf("cannot start service with error: %s", err)
	}
	service.DPrintf("service started successfully at: %s", serv.Addr)

	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, os.Interrupt)
	<-terminate
	service.DPrintf("service %s exited", serv.Addr)
}
