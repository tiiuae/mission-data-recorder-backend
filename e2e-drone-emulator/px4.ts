import * as rclnodejs from "rclnodejs";
import * as geolib from "geolib";
import * as api from "./px4-api";
import * as chalk from "chalk";

export async function runPX4(args: string[]): Promise<(() => void)> {
    const deviceNames = args.filter(x => !x.startsWith("-"));
    const fast = !args.includes("--slow");

    if (deviceNames.length == 0) {
        return;
    }
  
    await rclnodejs.init();

    const quit = deviceNames.map(x => runPX4node(x, fast));

    return () => {
        quit.forEach(q => q());
    }
}

function runPX4node(deviceName: string, fast: boolean): () => void {
    console.log(`Starting PX4: ${deviceName} (mode = ${fast ? "fast" : "slow"})`);
    const logcmd = (message: string):void =>
        console.log(`${chalk.green(`${deviceName}: ${message}`)} ${chalk.yellow(`[ ${armingStateMap[armingState]} | ${navigationStateMap[navState]} ]`)}`);
    const logev = (message: string):void =>
        console.log(`${chalk.white(`${deviceName}: ${message}`)} ${chalk.yellow(`[ ${armingStateMap[armingState]} | ${navigationStateMap[navState]} ]`)}`);
    const node = rclnodejs.createNode(`px4emu_${deviceName}`, deviceName);
    const pub = new api.PX4Api(node);
    const opt: rclnodejs.Options<rclnodejs.QoS.ProfileRef> = {
        enableTypedArray: true,
        qos: rclnodejs.QoS.profileSystemDefault,
        isRaw: false
    };

    let instanceCount = 1;
    let lat = 0.0;
    let lon = 0.0;
    let alt = 0.0;
    var armingState = 1;
    var navState = 4;

    let current: rclnodejs.nav_msgs.msg.Path = null;
    let queue: rclnodejs.nav_msgs.msg.Path[] = [];
    let tripleIndex: number = 0;
    
    // Run flight emulator
    const emulator = setInterval(() => {
        if (current == null && queue.length == 0) {
            return;
        }
        if (queue.length > 0) {
            if (current != null) {
                logev(`Mission aborted: instance ${instanceCount}`);
            }
            current = queue[0];
            queue = queue.slice(1);
            instanceCount++;
            tripleIndex = 0;

            if (armingState == 1) {
                logev(`New waypoints set: instance ${instanceCount}`);
            }
            else {
                logev(`Mission started: instance ${instanceCount}`);
            }
        }

        const count = current.poses.length * 3;
        const i = Math.floor(tripleIndex / 3);

        const targetLat = current.poses[i].pose.position.x;       
        const targetLon = current.poses[i].pose.position.y;
        const distanceToTarget = calculateDistance(lat, lon, targetLat, targetLon);
        if (distanceToTarget > 900) {
            logev(`MissionResult: instance ${instanceCount} failed: target too far (${distanceToTarget} meters)`);
            pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, false));
            // Simulate duplicate errors
            pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, false));
            pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, false));
            pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, false));
            pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, false));
            current = null;
            return;
        }
        else if (armingState == 1) {
            if (tripleIndex == 0) {
                // Mission valid
                logev(`MissionResult: instance ${instanceCount}, message ${tripleIndex+1} / ${count} (i = ${i}})`);
                pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, true));
                tripleIndex++;
            }
            // Not armed -> wait for 'start_mission'
            return;
        }


        lat = targetLat;
        lon = targetLon;
        alt = 25.0;

        logev(`MissionResult: instance ${instanceCount}, message ${tripleIndex+1} / ${count} (i = ${i}})`);
        pub.publishMissionResult(api.createMissionResult(instanceCount, tripleIndex, count, true));

        tripleIndex++;

        if (tripleIndex == count) {
            logev(`MissionResult: instance ${instanceCount} completed`);
            // Emulate weird behavior
            instanceCount++;
            pub.publishMissionResult(api.createMissionResult(instanceCount, -1, count, true));
            current = null;           
        }

    }, fast ? 100 : 1666);

    const subscriberPath = node.createSubscription(
        "nav_msgs/msg/Path",
        "path",
        opt,
        async (msg: rclnodejs.nav_msgs.msg.Path) => {
            logcmd(`Path received: ${msg.poses.length} waypoints`);
            queue.push(msg);
        }
    );
    
    
    const subscriberMavlink = node.createSubscription(
        "std_msgs/msg/String",
        "mavlinkcmd",
        opt,
        async (msg: rclnodejs.std_msgs.msg.String) => {
            if (msg.data == "start_mission") {
                armingState = 2;
                navState = 3;
            }
            if (msg.data == "land") {
                armingState = 1;
                navState = 4;
            }
            logcmd(`Mavlinkcmd received: ${msg.data}`);
        }
    );

    const subscriberMesh = node.createSubscription(
        "std_msgs/msg/String",
        "mesh_parameters",
        opt,
        async (msg: rclnodejs.std_msgs.msg.String) => {
            logcmd(`Mesh parameters received\n${msg.data}`);
        }
    );

    // Publish telemetry
    const publishers = setInterval(() => {
        pub.publishGlobalPosition(api.createGlobalPosition(lat, lon, alt));
        pub.publishLocalPosition(api.createLocalPosition(lat, lon, alt));
        pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
        pub.publishBatteryStatus(api.createBatteryStatus());
        pub.publishDebugValues("px4emu:text:hello world");
    }, 500);
        
    node.spin();
    
    return () => {
        clearInterval(publishers);
        clearInterval(emulator);
        node.stop();
    };
}

function calculateDistance(latFrom: number, lonFrom: number, latTo: number, lonTo: number): number {
    const from = { latitude: latFrom, longitude: lonFrom };
    const to = { latitude: latTo, longitude: lonTo }
    return geolib.getDistance(from, to);
}

enum NavStatus {
    Idle = "IDLE",
    Planning = "PLANNING",
    Commanding = "COMMANDING",
    Moving = "MOVING",
}

const armingStateMap = {
    0: "Init",
    1: "Standby",
    2: "Armed",
    3: "Standby error",
    4: "Shutdown",
    5: "In air restore",
};

const navigationStateMap = {
    0:  "Manual mode",
    1:  "Altitude control mode",
    2:  "Position control mode",
    3:  "Auto mission mode",
    4:  "Auto loiter mode",
    5:  "Auto return to launch mode",
    8:  "Auto land on engine failure",
    9:  "Auto land on gps failure",
    10: "Acro mode",
    12: "Descend mode",
    13: "Termination mode",
    14: "Offboard mode",
    15: "Stabilized mode",
    16: "Rattitude (aka \"flip\") mode",
    17: "Takeoff mode",
    18: "Land mode",
    19: "Follow mode",
    20: "Precision land with landing target",
    21: "Orbit mode",
};