package main

import (
	"fmt"
	"github.com/gswly/gomavlib"
	"github.com/gswly/gomavlib/dialects/common"
	"gopkg.in/alecthomas/kingpin.v2"
	"log"
	"os"
	"regexp"
	"time"
)

var Version string = "v0.0.0"

var reArgs = regexp.MustCompile("^([a-z]+):(.+)$")

type endpointType struct {
	args string
	desc string
	make func(args string) gomavlib.EndpointConf
}

var endpointTypes = map[string]endpointType{
	"serial": {
		"port:baudrate",
		"serial",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointSerial{args}
		},
	},
	"udps": {
		"listen_ip:port",
		"udp, server mode",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointUdpServer{args}
		},
	},
	"udpc": {
		"dest_ip:port",
		"udp, client mode",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointUdpClient{args}
		},
	},
	"udpb": {
		"broadcast_ip:port",
		"udp, broadcast mode",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointUdpBroadcast{BroadcastAddress: args}
		},
	},
	"tcps": {
		"listen_ip:port",
		"tcp, server mode",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointTcpServer{args}
		},
	},
	"tcpc": {
		"dest_ip:port",
		"tcp, client mode",
		func(args string) gomavlib.EndpointConf {
			return gomavlib.EndpointTcpClient{args}
		},
	},
}

type NodeId struct {
	SystemId    byte
	ComponentId byte
}

func initError(msg string, args ...interface{}) {
	fmt.Printf("ERROR: "+msg+"\n", args...)
	os.Exit(1)
}

func main() {
	kingpin.CommandLine.Help = "mavp2p " + Version + "\n\n" +
		"Link together Mavlink endpoints."

	printErrors := kingpin.Flag("print-errors", "print errors on screen.").Bool()

	hbDisable := kingpin.Flag("hb-disable", "disable periodic heartbeats").Bool()
	hbVersion := kingpin.Flag("hb-version", "set mavlink version of heartbeats").Default("1").Enum("1", "2")
	hbSystemId := kingpin.Flag("hb-systemid", "set system id of heartbeats. it is recommended to set a different system id for each router in the network.").Default("125").Int()
	hbPeriod := kingpin.Flag("hb-period", "set period of heartbeats").Default("5").Int()

	desc := "Space-separated list of endpoints. At least 2 endpoints are required. " +
		"Possible endpoints are:\n\n"
	for k, etype := range endpointTypes {
		desc += fmt.Sprintf("%s:%s (%s)\n\n", k, etype.args, etype.desc)
	}
	endpoints := kingpin.Arg("endpoints", desc).Strings()

	// print usage if no args are provided
	if len(os.Args) <= 1 {
		kingpin.Usage()
		os.Exit(1)
	}

	kingpin.Parse()

	if len(*endpoints) < 2 {
		initError("at least 2 endpoints are required")
	}

	var econfs []gomavlib.EndpointConf
	for _, e := range *endpoints {
		matches := reArgs.FindStringSubmatch(e)
		if matches == nil {
			initError("invalid endpoint: %s", e)
		}
		key, args := matches[1], matches[2]

		etype, ok := endpointTypes[key]
		if ok == false {
			initError("invalid endpoint: %s", e)
		}

		econfs = append(econfs, etype.make(args))
	}

	// decode/encode only heartbeat
	// needed for heartbeat to work
	dialect, err := gomavlib.NewDialect([]gomavlib.Message{
		&common.MessageHeartbeat{},
	})
	if err != nil {
		initError(err.Error())
	}

	node, err := gomavlib.NewNode(gomavlib.NodeConf{
		Endpoints: econfs,
		Dialect:   dialect,
		Version: func() gomavlib.NodeVersion {
			if *hbVersion == "2" {
				return gomavlib.V2
			}
			return gomavlib.V1
		}(),
		SystemId:          byte(*hbSystemId),
		ComponentId:       1,
		HeartbeatDisable:  *hbDisable,
		HeartbeatPeriod:   (time.Duration(*hbPeriod) * time.Second),
		ReturnParseErrors: true,
	})
	if err != nil {
		initError(err.Error())
	}
	defer node.Close()

	log.Printf("mavp2p %s", Version)
	log.Printf("router started with %d endpoints", len(econfs))

	nodes := make(map[NodeId]struct{})
	errorCount := 0

	if *printErrors == false {
		go func() {
			for {
				time.Sleep(5 * time.Second)
				if errorCount > 0 {
					log.Printf("%d errors in the last 5 seconds", errorCount)
					errorCount = 0
				}
			}
		}()
	}

	for {
		// wait until a message is received.
		res, ok := node.Read()
		if ok == false {
			break
		}

		if res.Error != nil {
			if *printErrors == true {
				log.Printf("err: %s", res.Error)
			} else {
				errorCount++
			}
			continue
		}

		// display message if node is new
		nodeId := NodeId{
			SystemId:    res.SystemId(),
			ComponentId: res.ComponentId(),
		}
		if _, ok := nodes[nodeId]; !ok {
			nodes[nodeId] = struct{}{}
			log.Printf("new node (sid=%d, cid=%d)", res.SystemId(), res.ComponentId())
		}

		// route message to every other channel
		node.WriteFrameExcept(res.Channel, res.Frame)
	}
}
