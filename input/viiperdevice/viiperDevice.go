package viiperdevice

import (
	"context"
	"encoding"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Alia5/VIIPER/viiperclient"
)

const stateBufferSize = 32

type Type string

const (
	DeviceTypeUnknown       Type = "unknown"
	DeviceTypeXbox360       Type = "xbox360"
	DeviceTypeDualShock4    Type = "dualshock4"
	DeviceTypeDualSense     Type = "dualsense"
	DeviceTypeDualSenseEdge Type = "dualsenseedge"
	DeviceTypeNS2Pro        Type = "ns2pro"
	DeviceTypeKeyboard      Type = "keyboard"
	DeviceTypeMouse         Type = "mouse"
)

type Device struct {
	controlStream *viiperclient.DeviceStream
	deviceInfo    *viiperclient.Device

	closeFunc func() error
	closeOnce sync.Once

	DeviceCtx       context.Context
	deviceCtxCancel context.CancelFunc

	FeedbackCh    <-chan encoding.BinaryUnmarshaler
	FeedbackErrCh <-chan error

	state     encoding.BinaryMarshaler
	stateChan chan []byte
	done      chan struct{}

	droppedStates   atomic.Uint64
	lastDropLogNano atomic.Int64
}

func New(
	controlStream *viiperclient.DeviceStream,
	deviceInfo *viiperclient.Device,
	closeFunc func() error,
) *Device {
	stateChan := make(chan []byte, stateBufferSize)
	deviceCtx, cancel := context.WithCancel(context.Background())

	decodeFeedback := readUnknownFeedback
	switch Type(deviceInfo.Type) {
	case DeviceTypeKeyboard:
		decodeFeedback = readKeyboardFeedback
	case DeviceTypeDualShock4:
		decodeFeedback = readDualShock4Feedback
	case DeviceTypeXbox360:
		decodeFeedback = readXbox360Feedback
	case DeviceTypeDualSense, DeviceTypeDualSenseEdge:
		decodeFeedback = readDualSenseFeedback
	case DeviceTypeNS2Pro:
		decodeFeedback = readNS2ProFeedback
	}

	feedbackCh, errCh := controlStream.StartReading(deviceCtx, stateBufferSize, decodeFeedback)

	d := &Device{
		controlStream:   controlStream,
		deviceInfo:      deviceInfo,
		closeFunc:       closeFunc,
		stateChan:       stateChan,
		DeviceCtx:       deviceCtx,
		deviceCtxCancel: cancel,
		FeedbackCh:      feedbackCh,
		FeedbackErrCh:   errCh,
		done:            make(chan struct{}),
	}
	go d.handleState()
	return d
}

func (d *Device) Info() viiperclient.Device {
	return *d.deviceInfo
}

func (d *Device) Type() Type {
	return Type(d.deviceInfo.Type)
}

func (d *Device) State() *encoding.BinaryMarshaler {
	return &d.state
}

// func (d *Device) UpdateFromSDLGamepad(gp *sdl.Gamepad) {
// 	if gp == nil {
// 		slog.Warn("Attempted to update VIIPER device with nil gamepad")
// 		return
// 	}

// 	switch DeviceType(d.deviceInfo.Type) {
// 	case DeviceTypeXbox360:
// 		toXbox360State(gp, &d.state)
// 	case DeviceTypeDualShock4:
// 		toDualShock4State(gp, &d.state)
// 	// case DeviceTypeKeyboard:
// 	// 	state = toKeyboardState(gp)
// 	default:
// 		slog.Warn("Cant update unknown VIIPER device type", "device_type", d.deviceInfo.Type)
// 		return
// 	}
// }

func (d *Device) QueueStateSend() {
	if d.state == nil {
		slog.Warn("No VIIPER state available to marshal", "device_type", d.deviceInfo.Type)
		return
	}

	if d.IsClosed() {
		slog.Warn("Attempted to update VIIPER device after it was closed")
		return
	}

	data, err := d.state.MarshalBinary()
	if err != nil {
		slog.Error("Failed to marshal VIIPER device state", "error", err)
		return
	}

	select {
	case <-d.done:
		slog.Warn("Attempted to update VIIPER device after it was closed")
		return
	case d.stateChan <- data:
		return
	default:
	}

	// Buffer full: drop the oldest pending packet and queue the freshest state,
	// so the latest input (e.g. a button release) is never the one lost.
	select {
	case <-d.stateChan:
	default:
	}
	select {
	case d.stateChan <- data:
	default:
	}
	d.noteDroppedState()
}

// noteDroppedState counts dropped state packets and logs at most once per second.
func (d *Device) noteDroppedState() {
	dropped := d.droppedStates.Add(1)
	now := time.Now().UnixNano()
	last := d.lastDropLogNano.Load()
	if now-last < int64(time.Second) || !d.lastDropLogNano.CompareAndSwap(last, now) {
		return
	}
	slog.Warn("VIIPER state buffer full, dropped oldest pending state",
		"device_type", d.deviceInfo.Type,
		"dropped_total", dropped,
	)
}

func (d *Device) handleState() {
	stream := d.controlStream

	for {
		select {
		case <-d.done:
			return
		case data := <-d.stateChan:
			n, err := stream.Write(data)
			if err == nil && n != len(data) {
				err = fmt.Errorf("short write: wrote %d of %d bytes", n, len(data))
			}
			if err != nil {
				slog.Error("Failed to send state to VIIPER device", "error", err)
				d.Close() //nolint
				return
			}
		}
	}
}

func (d *Device) IsClosed() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

func (d *Device) Close() error {
	var err error
	d.closeOnce.Do(func() {
		close(d.done)
		d.deviceCtxCancel()

		if d.controlStream != nil {
			err = d.controlStream.Close()
			d.controlStream = nil
		}

		if d.closeFunc != nil {
			go func(closeFunc func() error) {
				err := closeFunc()
				if err != nil {
					slog.Error("Failed to remove VIIPER device", "error", err)
				}
			}(d.closeFunc)
		}
	})

	return err
}
