// Package mqtt xk6 extenstion to suppor mqtt with k6
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/dop251/goja"
	"github.com/eclipse/paho.golang/autopaho"
	paho "github.com/eclipse/paho.golang/paho"
	"github.com/mstoykov/k6-taskqueue-lib/taskqueue"

	// "go.uber.org/zap"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
)

// To preserve the connection
type client struct {
	vu         modules.VU
	metrics    *mqttMetrics
	conf       conf
	obj        *goja.Object // the object that is given to js to interact with the WebSocket
	tq              *taskqueue.TaskQueue // get rid of
	connectionManager *autopaho.ConnectionManager
	clientConfig 	autopaho.ClientConfig
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

	must(client.obj.DefineDataProperty(
		"connect", rt.ToValue(client.Connect), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))
	must(client.obj.DefineDataProperty(
		"publish", rt.ToValue(client.Publish), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))

	must(client.obj.DefineDataProperty(
		"close", rt.ToValue(client.Close), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE))

	return client.obj
}

// Connect create a connection to mqtt
func (c *client) Connect() error {
	ctx := context.Background() // dont put timeout unless reason
	// defer cancel() // experiment witht his out

	parsed_urls := []*url.URL{}
	for _, server := range c.conf.servers {
		parsed_url, err := url.Parse(server)
		if err != nil {
			panic(err)
		}
		parsed_urls = append(parsed_urls, parsed_url)
	}



	cliCfg := autopaho.ClientConfig{
		ServerUrls: parsed_urls,
		KeepAlive:  20, // Keepalive message should be sent every 20 seconds
		// CleanStartOnInitialConnection defaults to false. Setting this to true will clear the session on the first connection.
		CleanStartOnInitialConnection: false,
		// SessionExpiryInterval - Seconds that a session will survive after disconnection.
		// It is important to set this because otherwise, any queued messages will be lost if the connection drops and
		// the server will not queue messages while it is down. The specific setting will depend upon your needs
		// (60 = 1 minute, 3600 = 1 hour, 86400 = one day, 0xFFFFFFFE = 136 years, 0xFFFFFFFF = don't expire)
		SessionExpiryInterval: 60,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {},
		OnConnectError: func(err error) { fmt.Println("Encountered a connection error: %s\n", err); panic(err) },
		// eclipse/paho.golang/paho provides base mqtt functionality, the below config will be passed in for each connection
		ClientConfig: paho.ClientConfig{
			// If you are using QOS 1/2, then it's important to specify a client id (which must be unique)
			ClientID: c.conf.clientid,
			OnClientError: func(err error) { fmt.Printf("client error: %s\n", err) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				if d.Properties != nil {
					fmt.Printf("server requested disconnect: %s\n", d.Properties.ReasonString)
				} else {
					fmt.Printf("server requested disconnect; reason code: %d\n", d.ReasonCode)
				}
			},
		},
	}
	cliCfg.ConnectUsername = c.conf.user
	cliCfg.ConnectPassword = []byte(c.conf.password)

	var err error
	c.connectionManager, err = autopaho.NewConnection(ctx, cliCfg)
	if err != nil {
		panic(err)
	}
	if err = c.connectionManager.AwaitConnection(ctx); err != nil {
		fmt.Println("Failed to connect to mqtt broker")
		panic(err)
	}

	c.clientConfig = cliCfg

	return nil
}

// Close the given client
// wait for pending connections for timeout (ms) before closing
func (c *client) Close() {
	// exit subscribe task queue if running
	ctx := context.Background()
	c.connectionManager.Disconnect(ctx) // use disconnect
	if c.tq != nil {
		c.tq.Close()
	}
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
