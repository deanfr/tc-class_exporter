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
    Class    string `json:"class"`
    Handle   string `json:"handle"`
    Root     bool   `json:"root"`
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
        log.Fatalf("Usage: %s -p <port> <interface>", os.Args[0])
    }
    nic := flag.Arg(0)

    registry := prometheus.NewRegistry()

    // Create GaugeVec for class fields
    rootGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_root",
        Help: "parent qdisc is root qdisc",
    }, []string{"class", "handle"})
    prioGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_prio",
        Help: "class priority of leaf; lower are served first",
    }, []string{"class", "handle"})
    rateGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_rate",
        Help: "rate allocated to this class (htb class can still borrow)",
    }, []string{"class", "handle"})
    ceilGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_ceil",
        Help: "rate at which the class can send if its parent has bandwidth to spare (htb)",
    }, []string{"class", "handle"})
    burstGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_burst",
        Help: "bytes that can be burst at ceil speed {computed}",
    }, []string{"class", "handle"})
    cburstGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_cburst",
        Help: "bytes that can be burst at 'infinite' speed {computed}",
    }, []string{"class", "handle"})
    // Create GaugeVec for stats fields
    statsBytesGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_bytes",
        Help: "number of seen bytes",
    }, []string{"class", "handle"})
    statsPacketsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_packets",
        Help: "number of seen packets",
    }, []string{"class", "handle"})
    statsDropsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_drops",
        Help: "number of dropped packets",
    }, []string{"class", "handle"})
    statsOverlimitsGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_overlimits",
        Help: "number of enqueues over the limit",
    }, []string{"class", "handle"})
    statsRequeuesGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_requeues",
        Help: "number of requeues",
    }, []string{"class", "handle"})
    statsLendedGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_lended",
        Help: "lended tokens (htb)",
    }, []string{"class", "handle"})
    statsBorrowedGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
        Name: "tc_class_stats_borrowed",
        Help: "borrowed tokens (htb)",
    }, []string{"class", "handle"})

    registry.MustRegister(rootGauge)
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
        // Execute the command and capture the output
        cmd := exec.Command("/usr/sbin/tc", "-s", "-j", "class", "show", "dev", nic)
        output, err := cmd.Output()
        if err != nil {
            http.Error(w, fmt.Sprintf("Failed to execute command: %v", err), http.StatusInternalServerError)
            return
        }

        // Parse the JSON output into a slice of Class structs
        var classes []Class
        err = json.Unmarshal(output, &classes)
        if err != nil {
            http.Error(w, fmt.Sprintf("Failed to parse JSON: %v", err), http.StatusInternalServerError)
            return
        }

        // Reset all metrics to zero before updating
        rootGauge.Reset()
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

        for _, class := range classes {
            rootGauge.WithLabelValues(class.Class, class.Handle).Set(boolToFloat64(class.Root))
            prioGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Prio))
            rateGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Rate))
            ceilGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Ceil))
            burstGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Burst))
            cburstGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Cburst))
            // Set stats fields
            statsBytesGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Bytes))
            statsPacketsGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Packets))
            statsDropsGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Drops))
            statsOverlimitsGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Overlimits))
            statsRequeuesGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Requeues))
            statsLendedGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Lended))
            statsBorrowedGauge.WithLabelValues(class.Class, class.Handle).Set(float64(class.Stats.Borrowed))
        }

        // Collect and write the metrics to the response writer
        h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
        h.ServeHTTP(w, r)
    })

    // Start the HTTP server to expose metrics
    log.Printf("Starting HTTP server on port %d", *port)
    log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}

// Helper function to convert bool to float64
func boolToFloat64(b bool) float64 {
    if b {
        return 1.0
    }
    return 0.0
}
