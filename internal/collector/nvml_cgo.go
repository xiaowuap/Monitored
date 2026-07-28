//go:build cgo

package collector

import (
	"fmt"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/monitored-io/monitored/internal/domain"
)

func nvmlAvailable() bool {
	if result := nvml.Init(); result != nvml.SUCCESS {
		return false
	}
	_ = nvml.Shutdown()
	return true
}

func collectNVML(host string) (domain.IngestBatch, error) {
	if result := nvml.Init(); result != nvml.SUCCESS {
		return domain.IngestBatch{AgentID: host}, fmt.Errorf("%w: %s", errNVMLUnavailable, nvml.ErrorString(result))
	}
	defer nvml.Shutdown()
	count, result := nvml.DeviceGetCount()
	if result != nvml.SUCCESS {
		return domain.IngestBatch{AgentID: host}, fmt.Errorf("NVML device count: %s", nvml.ErrorString(result))
	}
	hostID := "host:" + stableHash(host)
	batch := domain.IngestBatch{AgentID: host}
	now := time.Now().UTC()
	driverVersion, _ := nvml.SystemGetDriverVersion()
	for index := 0; index < count; index++ {
		device, status := nvml.DeviceGetHandleByIndex(index)
		if status != nvml.SUCCESS {
			continue
		}
		uuid, status := device.GetUUID()
		if status != nvml.SUCCESS || uuid == "" {
			continue
		}
		name, _ := device.GetName()
		assetID := "gpu:" + uuid
		metadata := map[string]any{
			"uuid": uuid, "collector": "nvml", "index": index,
			"driverVersion": driverVersion,
		}
		if value, status := device.GetVbiosVersion(); status == nvml.SUCCESS {
			metadata["vbiosVersion"] = value
		}
		if value, status := device.GetPciInfo(); status == nvml.SUCCESS {
			metadata["pciBusId"] = nvmlCString(value.BusId[:])
		}
		if current, pending, status := device.GetMigMode(); status == nvml.SUCCESS {
			metadata["migCurrent"] = current
			metadata["migPending"] = pending
		}
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: hostID, Kind: "gpu", Name: name,
			Vendor: "NVIDIA", Model: name, Metadata: metadata,
		})
		if value, status := device.GetTemperature(nvml.TEMPERATURE_GPU); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/temperature.gpu", "temperature.gpu",
				"GPU temperature", "temperature", "Cel", false, 10, float64(value), now, nil)
		}
		if value, status := device.GetFanSpeed(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/fan.speed", "fan.speed",
				"Fan speed", "fan", "%", false, 10, float64(value), now, nil)
		}
		if value, status := device.GetPowerUsage(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/power.draw", "power.draw",
				"Power draw", "power", "W", false, 10, float64(value)/1000, now, nil)
		}
		if value, status := device.GetTotalEnergyConsumption(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/energy.total", "energy.total",
				"Total energy", "energy", "J", true, 10, float64(value)/1000, now, nil)
		}
		if value, status := device.GetPowerManagementLimit(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/power.limit", "power.limit",
				"Power limit", "power", "W", false, 10, float64(value)/1000, now, nil)
		}
		if value, status := device.GetUtilizationRates(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/utilization.gpu", "utilization.gpu",
				"GPU utilization", "utilization", "%", false, 10, float64(value.Gpu), now, nil)
			appendNumeric(&batch, assetID, "nvml", uuid+"/utilization.memory", "utilization.memory",
				"Memory utilization", "utilization", "%", false, 10, float64(value.Memory), now, nil)
		}
		if value, status := device.GetMemoryInfo(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/memory.used", "memory.used",
				"Memory used", "capacity", "By", false, 10, float64(value.Used), now, nil)
			appendNumeric(&batch, assetID, "nvml", uuid+"/memory.total", "memory.total",
				"Memory total", "capacity", "By", false, 10, float64(value.Total), now, nil)
		}
		if value, status := device.GetClockInfo(nvml.CLOCK_SM); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/clocks.sm", "clocks.sm",
				"SM clock", "clock", "MHz", false, 10, float64(value), now, nil)
		}
		if value, status := device.GetClockInfo(nvml.CLOCK_MEM); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/clocks.memory", "clocks.memory",
				"Memory clock", "clock", "MHz", false, 10, float64(value), now, nil)
		}
		if value, status := device.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/ecc.uncorrected.volatile", "ecc.uncorrected.volatile",
				"Uncorrected volatile ECC", "error_count", "1", true, 10, float64(value), now, nil)
		}
		for _, retired := range []struct {
			cause nvml.PageRetirementCause
			key   string
			label string
		}{
			{nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS, "retired_pages.single_bit", "Retired pages (single-bit ECC)"},
			{nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR, "retired_pages.double_bit", "Retired pages (double-bit ECC)"},
		} {
			if pages, status := device.GetRetiredPages(retired.cause); status == nvml.SUCCESS {
				appendNumeric(&batch, assetID, "nvml", uuid+"/"+retired.key, retired.key,
					retired.label, "error_count", "1", true, 30, float64(len(pages)), now, nil)
			}
		}
		if value, status := device.GetRetiredPagesPendingStatus(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/retired_pages.pending", "retired_pages.pending",
				"Retired pages pending", "health", "1", false, 30, float64(value), now, nil)
		}
		for _, throughput := range []struct {
			counter nvml.PcieUtilCounter
			key     string
			label   string
		}{
			{nvml.PCIE_UTIL_TX_BYTES, "pcie.tx", "PCIe transmit throughput"},
			{nvml.PCIE_UTIL_RX_BYTES, "pcie.rx", "PCIe receive throughput"},
		} {
			if value, status := device.GetPcieThroughput(throughput.counter); status == nvml.SUCCESS {
				appendNumeric(&batch, assetID, "nvml", uuid+"/"+throughput.key, throughput.key,
					throughput.label, "throughput", "By/s", false, 10, float64(value)*1024, now, nil)
			}
		}
		if value, status := device.GetCurrentClocksThrottleReasons(); status == nvml.SUCCESS {
			appendNumeric(&batch, assetID, "nvml", uuid+"/clocks.throttle_reasons", "clocks.throttle_reasons",
				"Clock throttle reasons", "health_status", "1", false, 10, float64(value), now, nil)
		}
	}
	return batch, nil
}

func nvmlCString(value []int8) string {
	raw := make([]byte, 0, len(value))
	for _, character := range value {
		if character == 0 {
			break
		}
		raw = append(raw, byte(character))
	}
	return string(raw)
}
