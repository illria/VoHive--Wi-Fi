package device

import (
	"testing"
	"time"

	"github.com/iniwex5/vohive/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAddWorkerQMIManagedRebindsByIMEIWhenControlDeviceGone(t *testing.T) {
	// QMI 托管设备:配置 control_device 指向不存在节点,但配置了正确 IMEI;
	// 注入一块带该 IMEI 的新路径 QMI 硬件。bootstrap 应按 IMEI 取回新路径并采纳。

	originalDiscover := discoverQMIDevicesFn
	defer func() { discoverQMIDevicesFn = originalDiscover }()
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		return []QMIDevice{
			{
				ControlPath:  "/dev/cdc-wdm-new-qmi",
				NetInterface: "wwan-new",
				USBPath:      "1-2.3",
				ATPort:       "/dev/ttyUSB-new",
			},
		}, nil
	}

	originalResolveQMI := resolveDiscoveredQMIDeviceFn
	defer func() { resolveDiscoveredQMIDeviceFn = originalResolveQMI }()
	resolveDiscoveredQMIDeviceFn = func(dev QMIDevice, timeout time.Duration, allowProbe bool) (QMIDevice, string) {
		if dev.ControlPath == "/dev/cdc-wdm-new-qmi" {
			return dev, "123456789012345"
		}
		return dev, ""
	}

	// 初始化 Pool
	p := NewPool(&config.Config{})

	devCfg := config.DeviceConfig{
		ID:             "dev-qmi-1",
		DeviceBackend:  "qmi",
		ModemIMEI:      "123456789012345",
		ControlDevice:  "/dev/nonexistent-control-old",
		Interface:      "wwan-old",
		USBPath:        "1-9.9",
		NetworkEnabled: true, // hasManagedQMINetwork 的条件
	}

	// 此时 /dev/nonexistent-control-old 不存在，controlDeviceStatErr != nil，
	// 但发现流程应按 IMEI 取回新路径。Modem 管理器允许 QMI Core 异步等待
	// 设备节点出现，所以这里应成功返回 Worker，而不是依赖底层构造函数报错。
	defer p.cancel()
	w, err := p.AddWorkerFromConfig(devCfg)
	require.NoError(t, err)
	require.NotNil(t, w)
	require.Equal(t, "/dev/cdc-wdm-new-qmi", w.Config.ControlDevice)
	require.Equal(t, "wwan-new", w.Config.Interface)
	require.NoError(t, p.RemoveWorker(devCfg.ID))
}
