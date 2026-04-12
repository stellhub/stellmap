package replication

import "github.com/prometheus/client_golang/prometheus"

// Collector 将复制状态暴露为标准 Prometheus 指标。
type Collector struct {
	tracker                  *Tracker
	connectedDesc            *prometheus.Desc
	lastAppliedRevisionDesc  *prometheus.Desc
	lastSnapshotRevisionDesc *prometheus.Desc
	lastSyncUnixDesc         *prometheus.Desc
	errorTotalDesc           *prometheus.Desc
}

// NewCollector 创建复制指标 collector。
func NewCollector(tracker *Tracker) *Collector {
	labels := []string{"source_region", "source_cluster", "namespace", "service"}
	return &Collector{
		tracker: tracker,
		connectedDesc: prometheus.NewDesc(
			"starmap_replication_connected",
			"Whether the replication stream is currently connected.",
			labels,
			nil,
		),
		lastAppliedRevisionDesc: prometheus.NewDesc(
			"starmap_replication_last_applied_revision",
			"Last applied replication revision.",
			labels,
			nil,
		),
		lastSnapshotRevisionDesc: prometheus.NewDesc(
			"starmap_replication_last_snapshot_revision",
			"Last snapshot revision used by replication.",
			labels,
			nil,
		),
		lastSyncUnixDesc: prometheus.NewDesc(
			"starmap_replication_last_sync_unixtime",
			"Last successful replication sync time in unix seconds.",
			labels,
			nil,
		),
		errorTotalDesc: prometheus.NewDesc(
			"starmap_replication_error_total",
			"Total replication errors.",
			labels,
			nil,
		),
	}
}

// Describe 实现 prometheus.Collector。
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connectedDesc
	ch <- c.lastAppliedRevisionDesc
	ch <- c.lastSnapshotRevisionDesc
	ch <- c.lastSyncUnixDesc
	ch <- c.errorTotalDesc
}

// Collect 实现 prometheus.Collector。
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.tracker == nil {
		return
	}

	for _, item := range c.tracker.List() {
		labels := []string{item.SourceRegion, item.SourceClusterID, item.Namespace, item.Service}
		connected := 0.0
		if item.Connected {
			connected = 1
		}
		ch <- prometheus.MustNewConstMetric(c.connectedDesc, prometheus.GaugeValue, connected, labels...)
		ch <- prometheus.MustNewConstMetric(c.lastAppliedRevisionDesc, prometheus.GaugeValue, float64(item.LastAppliedRevision), labels...)
		ch <- prometheus.MustNewConstMetric(c.lastSnapshotRevisionDesc, prometheus.GaugeValue, float64(item.LastSnapshotRevision), labels...)
		ch <- prometheus.MustNewConstMetric(c.lastSyncUnixDesc, prometheus.GaugeValue, float64(item.LastSyncUnix), labels...)
		ch <- prometheus.MustNewConstMetric(c.errorTotalDesc, prometheus.CounterValue, float64(item.ErrorCount), labels...)
	}
}
