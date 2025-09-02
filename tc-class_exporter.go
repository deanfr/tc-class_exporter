package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Stats struct {
	Bytes      uint64 `json:"bytes"`
	Packets    uint64 `json:"packets"`
	Drops      uint64 `json:"drops"`
	Overlimits uint64 `json:"overlimits"`
	Requeues   uint64 `json:"requeues"`
	Backlog    uint64 `json:"backlog"`
	Qlen       uint64 `json:"qlen"`
	Lended     uint64 `json:"lended"`
	Borrowed   uint64 `json:"borrowed"`
	Giants     uint64 `json:"giants"`
	Tokens     int64  `json:"tokens"`
	Ctokens    int64  `json:"ctokens"`
}

type Class struct {
	Kind   string `json:"class"`
	Handle string `json:"handle"`
	Root   bool   `json:"root"`
	Device string `json:"device"`
	Parent string `json:"parent"`
	Leaf   string `json:"leaf"`
	Prio   int    `json:"prio"`
	Rate   uint64 `json:"rate"`
	Ceil   uint64 `json:"ceil"`
	Burst  uint64 `json:"burst"`
	Cburst uint64 `json:"cburst"`
	Stats  Stats  `json:"stats"`
}

// Split metric registry for params and stats
var (
	statsRegistry  = prometheus.NewRegistry()
	paramsRegistry = prometheus.NewRegistry()

	// Params (static/low-frequency)
	prioGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_prio",
		Help: "class priority of leaf; lower are served first",
	}, []string{"kind", "handle", "parent", "device"})
	rateGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_rate",
		Help: "rate allocated to this class (htb class can still borrow)",
	}, []string{"kind", "handle", "parent", "device"})
	ceilGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_ceil",
		Help: "rate at which the class can send if its parent has bandwidth to spare (htb)",
	}, []string{"kind", "handle", "parent", "device"})
	burstGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_burst",
		Help: "bytes that can be burst at ceil speed {computed}",
	}, []string{"kind", "handle", "parent", "device"})
	cburstGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_cburst",
		Help: "bytes that can be burst at 'infinite' speed {computed}",
	}, []string{"kind", "handle", "parent", "device"})

	// Stats (dynamic/high-frequency)
	statsBytesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_bytes",
		Help: "number of seen bytes",
	}, []string{"kind", "handle", "parent", "device"})
	statsPacketsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_packets",
		Help: "number of seen packets",
	}, []string{"kind", "handle", "parent", "device"})
	statsDropsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_drops",
		Help: "number of dropped packets",
	}, []string{"kind", "handle", "parent", "device"})
	statsOverlimitsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_overlimits",
		Help: "number of enqueues over the limit",
	}, []string{"kind", "handle", "parent", "device"})
	statsRequeuesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_requeues",
		Help: "number of requeues",
	}, []string{"kind", "handle", "parent", "device"})
	statsLendedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_lended",
		Help: "lended tokens (htb)",
	}, []string{"kind", "handle", "parent", "device"})
	statsBorrowedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_borrowed",
		Help: "borrowed tokens (htb)",
	}, []string{"kind", "handle", "parent", "device"})
)

func collectMetrics(nics []string) ([]Class, error) {
	var classes []Class

	validNics := make([]string, 0, len(nics))

	for _, nic := range nics {
		if _, err := net.InterfaceByName(nic); err == nil {
			validNics = append(validNics, nic)
		}
	}

	for _, nic := range validNics {
		cmd := exec.Command("/usr/sbin/tc", "-name", "-s", "-j", "class", "show", "dev", nic)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("command error: %w", err)
		}
		err = json.Unmarshal(output, &classes)
		if err != nil {
			return nil, fmt.Errorf("json error: %w", err)
		}
		for i := range classes {
			classes[i].Device = nic
			if classes[i].Root {
				classes[i].Parent = "root"
			}
		}
	}
	return classes, nil
}

func main() {
	port := flag.Int("p", 9096, "Port to listen on")
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatalf("Usage: %s -p <port> <interfaces...>", os.Args[0])
	}
	nics := flag.Args()

	// Register params
	paramsRegistry.MustRegister(prioGauge, rateGauge, ceilGauge, burstGauge, cburstGauge)

	// Register metrics
	statsRegistry.MustRegister(
		statsBytesGauge, statsPacketsGauge, statsDropsGauge,
		statsOverlimitsGauge, statsRequeuesGauge,
		statsLendedGauge, statsBorrowedGauge,
	)

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		statsBytesGauge.Reset()
		statsPacketsGauge.Reset()
		statsDropsGauge.Reset()
		statsOverlimitsGauge.Reset()
		statsRequeuesGauge.Reset()
		statsLendedGauge.Reset()
		statsBorrowedGauge.Reset()

		classes, err := collectMetrics(nics)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, class := range classes {
			statsBytesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Bytes))
			statsPacketsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Packets))
			statsDropsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Drops))
			statsOverlimitsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Overlimits))
			statsRequeuesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Requeues))
			statsLendedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Lended))
			statsBorrowedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Borrowed))
		}
		promhttp.HandlerFor(statsRegistry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	http.HandleFunc("/params", func(w http.ResponseWriter, r *http.Request) {
		prioGauge.Reset()
		rateGauge.Reset()
		ceilGauge.Reset()
		burstGauge.Reset()
		cburstGauge.Reset()

		classes, err := collectMetrics(nics)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, class := range classes {
			prioGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Prio))
			rateGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Rate))
			ceilGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Ceil))
			burstGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Burst))
			cburstGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Cburst))
		}
		promhttp.HandlerFor(paramsRegistry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	log.Printf("Listening on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
