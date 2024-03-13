// Package mqtt xk6 extenstion to suppor mqtt with k6
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/dop251/goja"
	paho "github.com/eclipse/paho.golang/paho"
	"github.com/mstoykov/k6-taskqueue-lib/taskqueue"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
)

type client struct {
	vu         modules.VU
	metrics    *mqttMetrics
	conf       conf
	pahoClient paho.Client
	obj        *goja.Object // the object that is given to js to interact with the WebSocket

	// listeners
	// this return goja.value *and* error in order to return error on exception instead of panic
	// https://pkg.go.dev/github.com/dop251/goja#hdr-Functions
	messageListener func(goja.Value) (goja.Value, error)
	errorListener   func(goja.Value) (goja.Value, error)
	tq              *taskqueue.TaskQueue
	messageChan     chan paho.WillMessage
	subRefCount     int
}

type conf struct {
	// The list of URL of  MQTT server to connect to
	servers []string
	// A username to authenticate to the MQTT server
	user string
	// Password to match username
	password string
	// clean session setting
	cleansess bool
	// Client id for reader
	clientid string
	// timeout ms
	timeout uint
	// path to caRoot path
	caRootPath string
	// path to client cert file
	clientCertPath string
	// path to client cert key file
	clientCertKeyPath string
}

//nolint:nosnakecase // their choice not mine
func (m *MqttAPI) client(c goja.ConstructorCall) *goja.Object {
	serversArray := c.Argument(0)
	rt := m.vu.Runtime()
	if serversArray == nil || goja.IsUndefined(serversArray) {
		common.Throw(rt, errors.New("Client requires a server list"))
	}
	var servers []string
	var clientConf conf
	err := rt.ExportTo(serversArray, &servers)
	if err != nil {
		common.Throw(rt,
			fmt.Errorf("Client requires valid server list, but got %q which resulted in %w", serversArray, err))
	}
	clientConf.servers = servers
	userValue := c.Argument(1)
	if userValue == nil || goja.IsUndefined(userValue) {
		common.Throw(rt, errors.New("Client requires a user value"))
	}
	clientConf.user = userValue.String()
	passwordValue := c.Argument(2)
	if userValue == nil || goja.IsUndefined(passwordValue) {
		common.Throw(rt, errors.New("Client requires a password value"))
	}
	clientConf.password = passwordValue.String()
	cleansessValue := c.Argument(3)
	if cleansessValue == nil || goja.IsUndefined(cleansessValue) {
		common.Throw(rt, errors.New("Client requires a cleaness value"))
	}
	clientConf.cleansess = cleansessValue.ToBoolean()

	clientIDValue := c.Argument(4)
	if clientIDValue == nil || goja.IsUndefined(clientIDValue) {
		common.Throw(rt, errors.New("Client requires a clientID value"))
	}
	clientConf.clientid = clientIDValue.String()

	timeoutValue := c.Argument(5)
	if timeoutValue == nil || goja.IsUndefined(timeoutValue) {
		common.Throw(rt, errors.New("Client requires a timeout value"))
	}
	clientConf.timeout = uint(timeoutValue.ToInteger())

	// optional args
	if caRootPathValue := c.Argument(6); caRootPathValue == nil || goja.IsUndefined(caRootPathValue) {
		clientConf.caRootPath = ""
	} else {
		clientConf.caRootPath = caRootPathValue.String()
	}
	if clientCertPathValue := c.Argument(7); clientCertPathValue == nil || goja.IsUndefined(clientCertPathValue) {
		clientConf.clientCertPath = ""
	} else {
		clientConf.clientCertPath = clientCertPathValue.String()
	}
	if clientCertKeyPathValue := c.Argument(8); clientCertKeyPathValue == nil || goja.IsUndefined(clientCertKeyPathValue) {
		clientConf.clientCertKeyPath = ""
	} else {
		clientConf.clientCertKeyPath = clientCertKeyPathValue.String()
	}

	client := &client{
		vu:      m.vu,
		metrics: &m.metrics,
		conf:    clientConf,
		obj:     rt.NewObject(),
	}
	must := func(err error) {
		if err != nil {
			common.Throw(rt, err)
		}
	}

	// TODO add onmessage,onclose and so on
	must(client.obj.DefineDataProperty(
		"addEventListener", rt.ToValue(client.AddEventListener), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(client.obj.DefineDataProperty(
		"subContinue", rt.ToValue(client.SubContinue), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(client.obj.DefineDataProperty(
		"connect", rt.ToValue(client.Connect), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(client.obj.DefineDataProperty(
		"publish", rt.ToValue(client.Publish), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(client.obj.DefineDataProperty(
		"subscribe", rt.ToValue(client.Subscribe), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))

	must(client.obj.DefineDataProperty(
		"close", rt.ToValue(client.Close), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))

	return client.obj
}

// Connect create a connection to mqtt
// NOTE: Allows only ONE server to connect to.
func (c *client) Connect() error {
	var config paho.ClientConfig

	var conns []net.Conn
	for _, server := range c.conf.servers {
		conn, err := net.Dial("tcp", server)
		if (err != nil) {
			log.Fatal(err)
		}
		conns = append(conns, conn)
	}
	config.ClientID = c.conf.clientid
	config.Conn = conns[0] // ALLOWS ONLY ONE SERVER TO CONNECT TO
	config.PacketTimeout = time.Duration(c.conf.timeout)

	client := paho.NewClient(config)
	var ctx context.Context
	ctx, _ = context.WithCancelCause(ctx)
	var connect *paho.Connect
	connect.Username = c.conf.user
	connect.Password = []byte(c.conf.password)
	connect.ClientID = c.conf.clientid

	_, err := client.Connect(ctx, connect)
	rt := c.vu.Runtime()
	if err != nil {
		common.Throw(rt, err)
		return err
	}
	c.pahoClient = *client
	return nil
}

// Close the given client
// wait for pending connections for timeout (ms) before closing
func (c *client) Close() {
	// exit subscribe task queue if running
	if c.tq != nil {
		c.tq.Close()
	}

	var disconnect paho.Disconnect
	c.pahoClient.Disconnect(&disconnect)
}

// error event for async
//
//nolint:nosnakecase // their choice not mine
func (c *client) newErrorEvent(msg string) *goja.Object {
	rt := c.vu.Runtime()
	o := rt.NewObject()
	must := func(err error) {
		if err != nil {
			common.Throw(rt, err)
		}
	}

	must(o.DefineDataProperty("type", rt.ToValue("error"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(o.DefineDataProperty("message", rt.ToValue(msg), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	return o
}
