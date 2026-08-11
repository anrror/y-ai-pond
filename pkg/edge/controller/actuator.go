package controller

import (
	"github.com/anrror/y-ai-pond/pkg/edge/hal"
)

// FeedingDriver implements ActuatorDriver using a PWM bus for speed control
// and a GPIO bus for H-bridge direction (High = forward, Low = reverse).
type FeedingDriver struct {
	pwm     hal.PWMBus
	dir     hal.GPIOBus
	forward bool
	speed   float64
	health  hal.Health
}

// NewFeedingDriver creates a FeedingDriver with the given buses.
func NewFeedingDriver(pwm hal.PWMBus, dir hal.GPIOBus) *FeedingDriver {
	return &FeedingDriver{
		pwm:    pwm,
		dir:    dir,
		health: hal.StatusOK,
	}
}

// SetSpeed sets the PWM duty cycle. pct is clamped to [0, 100].
func (d *FeedingDriver) SetSpeed(pct float64) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if err := d.pwm.SetDutyCycle(pct); err != nil {
		d.health = hal.StatusError
		return err
	}
	d.speed = pct
	d.health = hal.StatusOK
	return nil
}

// SetDirection controls the H-bridge direction pin.
func (d *FeedingDriver) SetDirection(forward bool) error {
	d.forward = forward
	if forward {
		if err := d.dir.High(); err != nil {
			d.health = hal.StatusError
			return err
		}
	} else {
		if err := d.dir.Low(); err != nil {
			d.health = hal.StatusError
			return err
		}
	}
	d.health = hal.StatusOK
	return nil
}

// Stop sets the PWM duty cycle to 0.
func (d *FeedingDriver) Stop() error {
	if err := d.pwm.SetDutyCycle(0); err != nil {
		d.health = hal.StatusError
		return err
	}
	d.speed = 0
	d.health = hal.StatusOK
	return nil
}

// Status returns the actuator health.
func (d *FeedingDriver) Status() hal.Health {
	return d.health
}

// Speed returns the current speed setting [0, 100].
func (d *FeedingDriver) Speed() float64 {
	return d.speed
}

// Forward returns the current direction state.
func (d *FeedingDriver) Forward() bool {
	return d.forward
}
