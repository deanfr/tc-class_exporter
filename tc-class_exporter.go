package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/exec"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

type Stats struct {
    Bytes       uint64 `json:"bytes"`
    Packets     uint64 `json:"packets"`
    Drops       uint64 `json:"drops"`
    Overlimits  uint64 `json:"overlimits"`
    Requeues    uint64 `json:"requeues"`
    Backlog     uint64 `json:"backlog"`
    Qlen        uint64 `json:"qlen"`
    Lended      uint64 `json:"lended"`
    Borrowed    uint64 `json:"borrowed"`
    Giants      uint64 `json:"giants"`
    Tokens      int64  `json:"tokens"`
    Ctokens     int64  `json:"ctokens"`
}

type Class struct {
    Kind     string `json:"class"`
    Handle   string `json:"handle"`
    Root     bool   `json:"root"`
    Device   string `json:"device"`
    Parent   string `json:"parent"`
    Leaf     string `json:"leaf"`
    Prio     int    `json:"prio"`
    Rate     uint64 `json:"rate"`
    Ceil     uint64 `json:"ceil"`
    Burst    uint64 `json:"burst"`
    Cburst   uint64 `json:"cburst"`
    Stats    Stats  `json:"stats"`
}

func main() {
    port := flag.Int("p", 9096, "Port to listen on")
    flag.Parse()
    if flag.NArg() < 1 {
        log.Fatalf("Usage: %s -p <port> <interfaces...>", os.Args[0])
    }
    nics := flag.Args()

    registry := prometheus.NewRegistry()

    // Create GaugeVec for class fields
    prioGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_prio",
        Help: "class priority of leaf; lower are served first",
    }, []string{"kind", "handle", "parent", "device"})
    rateGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_rate",
        Help: "rate allocated to this class (htb class can still borrow)",
    }, []string{"kind", "handle", "parent", "device"})
    ceilGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_ceil",
        Help: "rate at which the class can send if its parent has bandwidth to spare (htb)",
    }, []string{"kind", "handle", "parent", "device"})
    burstGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_burst",
        Help: "bytes that can be burst at ceil speed {computed}",
    }, []string{"kind", "handle", "parent", "device"})
    cburstGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_cburst",
        Help: "bytes that can be burst at 'infinite' speed {computed}",
    }, []string{"kind", "handle", "parent", "device"})
    // Create GaugeVec for stats fields
    statsBytesGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_bytes",
        Help: "number of seen bytes",
    }, []string{"kind", "handle", "parent", "device"})
    statsPacketsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_packets",
        Help: "number of seen packets",
    }, []string{"kind", "handle", "parent", "device"})
    statsDropsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_drops",
        Help: "number of dropped packets",
    }, []string{"kind", "handle", "parent", "device"})
    statsOverlimitsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_overlimits",
        Help: "number of enqueues over the limit",
    }, []string{"kind", "handle", "parent", "device"})
    statsRequeuesGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_requeues",
        Help: "number of requeues",
    }, []string{"kind", "handle", "parent", "device"})
    statsLendedGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_lended",
        Help: "lended tokens (htb)",
    }, []string{"kind", "handle", "parent", "device"})
    statsBorrowedGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_borrowed",
        Help: "borrowed tokens (htb)",
    }, []string{"kind", "handle", "parent", "device"})

    registry.MustRegister(prioGauge)
    registry.MustRegister(rateGauge)
    registry.MustRegister(ceilGauge)
    registry.MustRegister(burstGauge)
    registry.MustRegister(cburstGauge)
    registry.MustRegister(statsBytesGauge)
    registry.MustRegister(statsPacketsGauge)
    registry.MustRegister(statsDropsGauge)
    registry.MustRegister(statsOverlimitsGauge)
    registry.MustRegister(statsRequeuesGauge)
    registry.MustRegister(statsLendedGauge)
    registry.MustRegister(statsBorrowedGauge)

    // Custom handler for /metrics endpoint
    http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
        var classes []Class

        // Reset all metrics to zero before updating
        prioGauge.Reset()
        rateGauge.Reset()
        ceilGauge.Reset()
        burstGauge.Reset()
        cburstGauge.Reset()
        statsBytesGauge.Reset()
        statsPacketsGauge.Reset()
        statsDropsGauge.Reset()
        statsOverlimitsGauge.Reset()
        statsRequeuesGauge.Reset()
        statsLendedGauge.Reset()
        statsBorrowedGauge.Reset()

        // Execute the command and capture the output
        for _, nic := range nics {
            cmd := exec.Command("/usr/sbin/tc", "-name", "-s", "-j", "class", "show", "dev", nic)
            output, err := cmd.Output()
            if err != nil {
                http.Error(w, fmt.Sprintf("Failed to execute command: %v", err), http.StatusInternalServerError)
                return
            }

            // Parse the JSON output into a slice of Class structs
            err = json.Unmarshal(output, &classes)
            if err != nil {
                http.Error(w, fmt.Sprintf("Failed to parse JSON: %v", err), http.StatusInternalServerError)
                return
            }

            for _, class := range classes {
                if class.Root {
                    class.Parent = "root"
                }
                class.Device = nic
                prioGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Prio))
                rateGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Rate))
                ceilGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Ceil))
                burstGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Burst))
                cburstGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Cburst))
                // Set stats fields
                statsBytesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Bytes))
                statsPacketsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Packets))
                statsDropsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Drops))
                statsOverlimitsGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Overlimits))
                statsRequeuesGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Requeues))
                statsLendedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Lended))
                statsBorrowedGauge.WithLabelValues(class.Kind, class.Handle, class.Parent, class.Device).Set(float64(class.Stats.Borrowed))
            }
        }

        // Collect and write the metrics to the response writer
        h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
        h.ServeHTTP(w, r)
    })

    // Start the HTTP server to expose metrics
    log.Printf("Starting HTTP server on port %d", *port)
    log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
