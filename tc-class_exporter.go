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

type Class struct {
	Device string `json:"device"`
	Parent string `json:"parent"`
	Kind   string `json:"class"`
	Handle string `json:"handle"`
	Root   bool   `json:"root"`
	Leaf   string `json:"leaf"`
	Prio   int    `json:"prio"`
	Rate   uint64 `json:"rate"`
	Ceil   uint64 `json:"ceil"`
	Burst  uint64 `json:"burst"`
	Cburst uint64 `json:"cburst"`
	Stats  Stats  `json:"stats"`
}

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

type Options struct {
	R2q               uint64 `json:"r2q"`
	DirectPacketsStat uint64 `json:"direct_packets_stat"`
	DirectQlen        uint64 `json:"direct_qlen"`
}

type Qdisc struct {
	Device     string  `json:"device"`
	Parent     string  `json:"parent"`
	Kind       string  `json:"class"`
	Handle     string  `json:"handle"`
	Root       bool    `json:"root"`
	Options    Options `json:"options"`
	Bytes      uint64  `json:"bytes"`
	Packets    uint64  `json:"packets"`
	Drops      uint64  `json:"drops"`
	Overlimits uint64  `json:"overlimits"`
	Requeues   uint64  `json:"requeues"`
	Backlog    uint64  `json:"backlog"`
	Qlen       uint64  `json:"qlen"`
}

// Split metric registry for params and stats
var (
	statsRegistry  = prometheus.NewRegistry()
	paramsRegistry = prometheus.NewRegistry()

	// Params (static/low-frequency)
	// Class
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
	// qdisc
	r2qGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_options_r2q",
		Help: "Divisor used to calculate quantum values for classes.  Classes divide rate by this number.",
	}, []string{"kind", "handle", "parent", "device"})
	direct_packets_statGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_options_direct_packets_stat",
		Help: "direct_packets_stat option",
	}, []string{"kind", "handle", "parent", "device"})
	direct_qlenGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_options_direct_qlen",
		Help: "direct_qlen option",
	}, []string{"kind", "handle", "parent", "device"})

	// Stats (dynamic/high-frequency)
	cstatsBytesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_bytes",
		Help: "number of seen bytes",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsPacketsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_packets",
		Help: "number of seen packets",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsDropsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_drops",
		Help: "number of dropped packets",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsOverlimitsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_overlimits",
		Help: "number of enqueues over the limit",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsRequeuesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_requeues",
		Help: "number of requeues",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsLendedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_lended",
		Help: "lended tokens (htb)",
	}, []string{"kind", "handle", "parent", "device"})
	cstatsBorrowedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_class_stats_borrowed",
		Help: "borrowed tokens (htb)",
	}, []string{"kind", "handle", "parent", "device"})

	qstatsBytesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_bytes",
		Help: "number of seen bytes",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsPacketsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_packets",
		Help: "number of seen packets",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsDropsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_drops",
		Help: "number of dropped packets",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsOverlimitsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_overlimits",
		Help: "number of enqueues over the limit",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsRequeuesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_requeues",
		Help: "number of requeues",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsBacklogGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_backlog",
		Help: "backlog size",
	}, []string{"kind", "handle", "parent", "device"})
	qstatsLenGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tc_qdisc_qlen",
		Help: "qlen size",
	}, []string{"kind", "handle", "parent", "device"})
)

func collectMetricsClasses(nics []string) ([]Class, error) {
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

func collectMetricsQdiscs(nics []string) ([]Qdisc, error) {
	var qdiscs []Qdisc

	validNics := make([]string, 0, len(nics))

	for _, nic := range nics {
		if _, err := net.InterfaceByName(nic); err == nil {
			validNics = append(validNics, nic)
		}
	}

	for _, nic := range validNics {
		cmd := exec.Command("/usr/sbin/tc", "-name", "-s", "-j", "qdisc", "show", "dev", nic)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("command error: %w", err)
		}
		err = json.Unmarshal(output, &qdiscs)
		if err != nil {
			return nil, fmt.Errorf("json error: %w", err)
		}
		for i := range qdiscs {
			qdiscs[i].Device = nic
			if qdiscs[i].Root {
				qdiscs[i].Parent = "root"
			}
		}
	}
	return qdiscs, nil
}

func main() {
	port := flag.Int("p", 9096, "Port to listen on")
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatalf("Usage: %s -p <port> <interfaces...>", os.Args[0])
	}
	nics := flag.Args()

	// Register params
	paramsRegistry.MustRegister(prioGauge, rateGauge, ceilGauge, burstGauge, cburstGauge, r2qGauge, direct_packets_statGauge, direct_qlenGauge)

	// Register metrics
	statsRegistry.MustRegister(
		cstatsBytesGauge, cstatsPacketsGauge, cstatsDropsGauge,
		cstatsOverlimitsGauge, cstatsRequeuesGauge,
		cstatsLendedGauge, cstatsBorrowedGauge,
		qstatsBytesGauge, qstatsPacketsGauge, qstatsDropsGauge,
		qstatsOverlimitsGauge, qstatsRequeuesGauge,
		qstatsBacklogGauge, qstatsLenGauge,
	)

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		cstatsBytesGauge.Reset()
		cstatsPacketsGauge.Reset()
		cstatsDropsGauge.Reset()
		cstatsOverlimitsGauge.Reset()
		cstatsRequeuesGauge.Reset()
		cstatsLendedGauge.Reset()
		cstatsBorrowedGauge.Reset()
		qstatsBytesGauge.Reset()
		qstatsPacketsGauge.Reset()
		qstatsDropsGauge.Reset()
		qstatsOverlimitsGauge.Reset()
		qstatsRequeuesGauge.Reset()
		qstatsBacklogGauge.Reset()
		qstatsLenGauge.Reset()

		classes, err := collectMetricsClasses(nics)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		qdiscs, err := collectMetricsQdiscs(nics)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, class := range classes {
			cstatsBytesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Bytes))
			cstatsPacketsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Packets))
			cstatsDropsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Drops))
			cstatsOverlimitsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Overlimits))
			cstatsRequeuesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Requeues))
			cstatsLendedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Lended))
			cstatsBorrowedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Borrowed))
		}
		for _, qdisc := range qdiscs {
			qstatsBytesGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Bytes))
			qstatsPacketsGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Packets))
			qstatsDropsGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Drops))
			qstatsOverlimitsGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Overlimits))
			qstatsRequeuesGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Requeues))
			qstatsBacklogGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Backlog))
			qstatsLenGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Qlen))
		}
		promhttp.HandlerFor(statsRegistry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	http.HandleFunc("/params", func(w http.ResponseWriter, r *http.Request) {
		prioGauge.Reset()
		rateGauge.Reset()
		ceilGauge.Reset()
		burstGauge.Reset()
		cburstGauge.Reset()
		r2qGauge.Reset()
		direct_packets_statGauge.Reset()
		direct_qlenGauge.Reset()

		classes, err := collectMetricsClasses(nics)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		qdiscs, err := collectMetricsQdiscs(nics)
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
		for _, qdisc := range qdiscs {
			r2qGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Options.R2q))
			direct_packets_statGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Options.DirectPacketsStat))
			direct_qlenGauge.WithLabelValues(qdisc.Kind, qdisc.Handle, qdisc.Parent, qdisc.Device).Set(float64(qdisc.Options.DirectQlen))
		}
		promhttp.HandlerFor(paramsRegistry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	log.Printf("Listening on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
