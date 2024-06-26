/*

This is a k6 test script that imports the xk6-mqtt and
tests Mqtt with a 100 messages per connection.

*/

import {
    check
} from 'k6';

const mqtt = require('k6/x/mqtt');

const rnd_count = 2000;

// create random number to create a new topic at each run
let rnd = Math.random() * rnd_count;

// conection timeout (ms)
let connectTimeout = 2000

// publish timeout (ms)
let publishTimeout = 2000

// subscribe timeout (ms)
let subscribeTimeout = 2000

// connection close timeout (ms)
let closeTimeout = 2000

// Mqtt topic one per VU
const k6Topic = "vehicle_state_ota/8b9dbede-27fc-485a-a55b-e20a72bcb257";
// Connect IDs one connection per VU
const k6SubId = `k6-sub-${rnd}-${__VU}`;
const k6PubId = `k6-pub-${rnd}-${__VU}`;

// number of message pusblished and receives at each iteration
const messageCount = 3;

const host = "mqtts://mqtt.mosaic.staging.applied.dev";
const port = "8883";


// create publisher client
console.log("in test.js, creating client")
let publisher = new mqtt.Client(
    // The list of URL of  MQTT server to connect to
    [host + ":" + port],
    // A username to authenticate to the MQTT server
    "admin-user",
    // Password to match username
    "oJs43SWfsUZn5gRPqNxC",
    // clean session setting
    false,
    // Client id for reader
    k6PubId,
    // timeout in ms
    connectTimeout,
)
let err;

const send_command_request = {
    vehicle_uuid: "8b9dbede-27fc-485a-a55b-e20a72bcb257",
    command_wrapper: {
      command: {
        blinker_dance: {
          run_blinker_dance: true,
        },
      },
      enqueue_time: new Date().toISOString(),
    },
  }

try {
    console.log("in test.js connecting to broker")
    publisher.connect()
    try {
        publisher.publish(
            // topic to be used
            k6Topic,
            // The QoS of messages
            1,
            // Message to be sent
            "Hello, k6!",
            // retain policy on message
            false,
            // timeout in ms
            publishTimeout,
        );
    } catch (error) {
        console.log("We failed to publish!: ", error)
        err_publish = error
    }
    console.log("back in test after connecting")
}
catch (error) {
    err = error
}

export default function () {
    return
    for (let i = 0; i < messageCount; i++) {
        // publish count messages
        let err_publish;
        // console.log("in test.js, will publish message")
        try {
            publisher.publish(
                // topic to be used
                k6Topic,
                // The QoS of messages
                1,
                // Message to be sent
                "Hello, k6!",
                // retain policy on message
                false,
                // timeout in ms
                publishTimeout,
            );
        } catch (error) {
            console.log("We failed to publish!: ", error)
            err_publish = error
        }
        check(err_publish, {
            "is sent": err => err == undefined
        });
    }
}

export function teardown() {
    // closing both connections at VU close
    publisher.close(closeTimeout);
    // subscriber.close(closeTimeout);
}
