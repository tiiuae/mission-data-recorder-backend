import * as rclnodejs from "rclnodejs";
import * as geolib from "geolib";
import * as api from "./f4f-api";
import * as chalk from "chalk";

export async function runF4F(args: string[]): Promise<(() => void)> {
    const deviceNames = args.filter(x => !x.startsWith("-"));
    const fast = !args.includes("--slow");

    if (deviceNames.length == 0) {
        return;
    }
  
    await rclnodejs.init();

    const quit = deviceNames.map(x => runF4Fnode(x, fast));

    return () => {
        quit.forEach(q => q());
    }
}

function runF4Fnode(deviceName: string, fast: boolean): () => void {
    console.log(`Starting F4F: ${deviceName} (mode = ${fast ? "fast" : "slow"})`);
    const logcmd = (message: string):void =>
        console.log(`${chalk.green(`${deviceName}: ${message}`)} ${chalk.yellow(`[ ${armingStateMap[armingState]} | ${navigationStateMap[navState]} | ${navStatus} ]`)}`);
    const logev = (message: string):void =>
        console.log(`${chalk.white(`${deviceName}: ${message}`)} ${chalk.yellow(`[ ${armingStateMap[armingState]} | ${navigationStateMap[navState]} | ${navStatus} ]`)}`);
    const controlNode = rclnodejs.createNode("control_interface", deviceName);
    const navigationNode = rclnodejs.createNode("navigation", deviceName);
    const pub = new api.F4FApi(navigationNode);
    const opt: rclnodejs.Options<rclnodejs.QoS.ProfileRef> = {
        enableTypedArray: true,
        qos: rclnodejs.QoS.profileSystemDefault,
        isRaw: false
    };

    const interval = fast ? 100 : 5000;

    var lat = 1.0;
    var lon = 1.0;
    var alt = 0.0;
    var armingState = 1;
    var navState = 4;
    var navStatus = NavStatus.Idle;
    var airborne = false;

    const seviceArming = controlNode.createService("std_srvs/srv/SetBool", "~/arming", opt, (req, res) => {
        armingState = req.data ? 2 : 1;
        const result = {
            success: true,
            message: armingState == 1 ? "Vehicle disarmed" : "Vehicle armed"
        } as rclnodejs.std_srvs.srv.SetBool_Response;

        logcmd(`ARM: ${req.data} -> ${result.message}`);
        res.send(result);
    });

    const serviceTakeoff = controlNode.createService("std_srvs/srv/Trigger", "~/takeoff", opt, (req, res) => {
        if (armingState == 2 && !airborne) {
            navState = 17;
            airborne = true;
            const result = {
                success: true,
                message: "Takeoff started"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
    
            logcmd(`TAKEOFF: ${req} -> ${result.message}`);
            res.send(result);
            setTimeout(handleTakeoff, interval);
            pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
        }
        else {
            const result = {
                success: false,
                message: "Takeoff rejected"
            } as rclnodejs.std_srvs.srv.SetBool_Response;

            logcmd(`TAKEOFF: ${req} -> ${result.message}`);
            res.send(result);
        }
    });

    const handleTakeoff = () => {
        if (navState == 17)
            navState = 3;

        logev(`TAKEOFF: completed`);
    };

    const serviceLand = controlNode.createService("std_srvs/srv/Trigger", "~/land", opt, (req, res) => {
        if (armingState == 2 || airborne) {
            navState = 18;
            const result = {
                success: true,
                message: "Landing"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`LAND: ${req} -> ${result.message}`);
            res.send(result);
            setTimeout(handleLanding, interval);
            pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
        }
        else {
            const result = {
                success: false,
                message: "Landing rejected"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`LAND: ${req} -> ${result.message}`);
            res.send(result);
        }
    });

    const handleLanding = () => {
        if (navState == 18) {
            armingState = 1;
            airborne = false;
        }

        logev(`LAND: completed`);
    };

    const serviceLocalWaypoint = navigationNode.createService("fog_msgs/srv/Vec4", "~/local_waypoint", opt, (req, res) => {
        if (armingState == 2 && (navState == 3 || navState == 4) && navStatus == NavStatus.Idle && airborne) {
            navState = 3;
            navStatus = NavStatus.Moving;
            const result = {
                success: true,
                message: "Navigation goal set"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`LOCAL WAYPOINT IN: ${req.goal} -> ${result.message}`);
            res.send(result);

            const [targetLon, targetLat] = calculateTarget(0, 0, req.goal[0], req.goal[1]);
            setTimeout(() => handleWaypoint(targetLat, targetLon), interval);
            pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
            pub.publishStatus(navStatus);
        }
        else {
            const result = {
                success: false,
                message: "Waypoint rejected. Vehicle not IDLE / Armed / Airborne"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`LOCAL WAYPOINT IN: ${req.goal} -> ${result.message}`);
            res.send(result);
        }
    });

    const serviceGlobalWaypoint = navigationNode.createService("fog_msgs/srv/Vec4", "~/gps_waypoint", opt, (req, res) => {
        if (armingState == 2 && (navState == 3 || navState == 4) && navStatus == NavStatus.Idle && airborne) {
            navState = 3;
            navStatus = NavStatus.Moving;
            const result = {
                success: true,
                message: "Navigation goal set"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`GPS WAYPOINT IN: ${req.goal} -> ${result.message}`);
            res.send(result);

            setTimeout(() => handleWaypoint(req.goal[0], req.goal[1]), interval);
            pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
            pub.publishStatus(navStatus);
        }
        else {
            const result = {
                success: false,
                message: "Waypoint rejected. Vehicle not IDLE / Armed / Airborne"
            } as rclnodejs.std_srvs.srv.SetBool_Response;
            logcmd(`GPS WAYPOINT IN: ${req.goal} -> ${result.message}`);
            res.send(result);
        }
    });

    const handleWaypoint = (targetLat: number, targetLon: number) => {
        if (navStatus == NavStatus.Moving)
            navStatus = NavStatus.Idle;

        logev(`WAYPOINT IN: completed: [ ${lat}, ${lon} ] -> [ ${targetLat}, ${targetLon} ]`);
        
        lon = targetLon;
        lat = targetLat;

        pub.publishGlobalPosition(api.createGlobalPosition(lat, lon, alt));
        pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
        pub.publishStatus(navStatus);
    };

    // Publish telemetry
    const publishers = setInterval(() => {
        pub.publishGlobalPosition(api.createGlobalPosition(lat, lon, alt));
        pub.publishLocalPosition(api.createLocalPosition(lat, lon, alt));
        pub.publishVechicleStatus(api.createVehicleStatus(armingState, navState));
        pub.publishBatteryStatus(api.createBatteryStatus());
        pub.publishDebugValues("f4femu:text:hello world");
        pub.publishStatus(navStatus);
    }, 500);

    controlNode.spin();
    navigationNode.spin();

    return () => {
        clearInterval(publishers);
        controlNode.stop();
        navigationNode.stop();
    };
}

function calculateTarget(lon: number, lat: number, x: number, y: number): [number, number] {
    const distance = Math.sqrt(
        Math.pow(Math.abs(x), 2) + Math.pow(Math.abs(y), 2)
    );
    const bearing = Math.atan2(x, y) * (180 / Math.PI);
    const res = geolib.computeDestinationPoint({ lon, lat }, distance, bearing);
    return [ res.longitude, res.latitude ];
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