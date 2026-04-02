package push

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"sync"

	"firebase.google.com/go/v4/messaging"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
)

type Notification struct {
	Title    string
	Body     string
	Token    string
	Platform string // "ios" or "android"
	Data     map[string]string
	Category string // notification category for iOS / channel for Android
}

type Result struct {
	Token    string
	Success  bool
	BadToken bool
	Err      error
}

type APNsConfig struct {
	KeyPath  string // path to .p8 file (local dev)
	KeyData  string // raw .p8 key content (for env var / Fly deployment)
	KeyID    string
	TeamID   string
	BundleID string
	Sandbox  bool   // use sandbox/development endpoint
}

type Dispatcher struct {
	apnsClient *apns2.Client
	fcmClient  *messaging.Client
	bundleID   string
	jobs       chan job
	wg         sync.WaitGroup
}

type job struct {
	notif    *Notification
	resultCh chan<- *Result
}

func NewDispatcher(apnsCfg *APNsConfig, fcmClient *messaging.Client, workers int) (*Dispatcher, error) {
	d := &Dispatcher{
		fcmClient: fcmClient,
		jobs:      make(chan job, 1000),
	}

	if apnsCfg != nil {
		var authKey *ecdsa.PrivateKey
		var err error
		if apnsCfg.KeyData != "" {
			authKey, err = token.AuthKeyFromBytes([]byte(apnsCfg.KeyData))
		} else {
			authKey, err = token.AuthKeyFromFile(apnsCfg.KeyPath)
		}
		if err != nil {
			return nil, fmt.Errorf("apns auth key: %w", err)
		}
		tok := &token.Token{
			AuthKey: authKey,
			KeyID:   apnsCfg.KeyID,
			TeamID:  apnsCfg.TeamID,
		}
		client := apns2.NewTokenClient(tok)
		if apnsCfg.Sandbox {
			d.apnsClient = client.Development()
		} else {
			d.apnsClient = client.Production()
		}
		d.bundleID = apnsCfg.BundleID
	}

	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d, nil
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for j := range d.jobs {
		var result *Result
		switch j.notif.Platform {
		case "ios":
			result = d.sendAPNs(j.notif)
		case "android":
			result = d.sendFCM(j.notif)
		default:
			result = &Result{Token: j.notif.Token, Err: fmt.Errorf("unknown platform: %s", j.notif.Platform)}
		}
		if j.resultCh != nil {
			j.resultCh <- result
		}
	}
}

func (d *Dispatcher) Send(notif *Notification) *Result {
	ch := make(chan *Result, 1)
	d.jobs <- job{notif: notif, resultCh: ch}
	return <-ch
}

func (d *Dispatcher) SendAsync(notif *Notification, resultCh chan<- *Result) {
	d.jobs <- job{notif: notif, resultCh: resultCh}
}

func (d *Dispatcher) Close() {
	close(d.jobs)
	d.wg.Wait()
}

func (d *Dispatcher) sendAPNs(notif *Notification) *Result {
	if d.apnsClient == nil {
		return &Result{Token: notif.Token, Err: fmt.Errorf("APNs not configured")}
	}

	p := payload.NewPayload().
		AlertTitle(notif.Title).
		AlertBody(notif.Body).
		Sound("default").
		MutableContent()

	if notif.Category != "" {
		p.Category(notif.Category)
	}
	for k, v := range notif.Data {
		p.Custom(k, v)
	}

	n := &apns2.Notification{
		DeviceToken: notif.Token,
		Topic:       d.bundleID,
		Payload:     p,
	}

	resp, err := d.apnsClient.Push(n)
	if err != nil {
		return &Result{Token: notif.Token, Err: err}
	}

	if resp.StatusCode != 200 {
		badToken := resp.Reason == apns2.ReasonBadDeviceToken ||
			resp.Reason == apns2.ReasonUnregistered ||
			resp.Reason == apns2.ReasonExpiredToken
		log.Printf("apns: token %s… status %d reason %s", notif.Token[:8], resp.StatusCode, resp.Reason)
		return &Result{Token: notif.Token, BadToken: badToken, Err: fmt.Errorf("apns: %s", resp.Reason)}
	}

	return &Result{Token: notif.Token, Success: true}
}

func (d *Dispatcher) sendFCM(notif *Notification) *Result {
	if d.fcmClient == nil {
		return &Result{Token: notif.Token, Err: fmt.Errorf("FCM not configured")}
	}

	msg := &messaging.Message{
		Token: notif.Token,
		Notification: &messaging.Notification{
			Title: notif.Title,
			Body:  notif.Body,
		},
		Data: notif.Data,
		Android: &messaging.AndroidConfig{
			Notification: &messaging.AndroidNotification{
				ChannelID: notif.Category,
				Sound:     "default",
			},
		},
	}

	_, err := d.fcmClient.Send(context.Background(), msg)
	if err != nil {
		badToken := messaging.IsUnregistered(err) || messaging.IsInvalidArgument(err)
		if badToken {
			log.Printf("fcm: bad token %s…: %v", notif.Token[:8], err)
		}
		return &Result{Token: notif.Token, BadToken: badToken, Err: err}
	}

	return &Result{Token: notif.Token, Success: true}
}
