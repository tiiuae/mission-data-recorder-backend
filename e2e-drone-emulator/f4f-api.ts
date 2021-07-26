import * as rclnodejs from "rclnodejs";

export class F4FApi {
    private publisherVehicleGlobalPosition: rclnodejs.Publisher<"px4_msgs/msg/VehicleGlobalPosition">;
    private publisherVehicleLocalPosition: rclnodejs.Publisher<"px4_msgs/msg/VehicleLocalPosition">;
    private publisherVehicleStatus: rclnodejs.Publisher<"px4_msgs/msg/VehicleStatus">;
    private publisherBatteryStatus: rclnodejs.Publisher<"px4_msgs/msg/BatteryStatus">;
    private publisherDebugValues: rclnodejs.Publisher<"std_msgs/msg/String">;
    private publisherSatus: rclnodejs.Publisher<"std_msgs/msg/String">;

    constructor(private node: rclnodejs.Node) {
        const opt: rclnodejs.Options<rclnodejs.QoS.ProfileRef> = {
            enableTypedArray: true,
            qos: rclnodejs.QoS.profileSystemDefault,
            isRaw: false
        };

        this.publisherVehicleGlobalPosition = this.node.createPublisher(
            "px4_msgs/msg/VehicleGlobalPosition",
            "VehicleGlobalPosition_PubSubTopic",
            opt
        );
    
        this.publisherVehicleLocalPosition = this.node.createPublisher(
            "px4_msgs/msg/VehicleLocalPosition",
            "VehicleLocalPosition_PubSubTopic",
            opt
        );
    
        this.publisherVehicleStatus = this.node.createPublisher(
            "px4_msgs/msg/VehicleStatus",
            "VehicleStatus_PubSubTopic",
            opt
        );
    
        this.publisherBatteryStatus = this.node.createPublisher(
            "px4_msgs/msg/BatteryStatus",
            "BatteryStatus_PubSubTopic",
            opt
        );

        this.publisherDebugValues = this.node.createPublisher(
            "std_msgs/msg/String",
            "debug_values",
            opt
        );

        this.publisherSatus = this.node.createPublisher(
            "std_msgs/msg/String",
            "~/status_out",
            opt
        );
    }

    publishGlobalPosition(msg: rclnodejs.px4_msgs.msg.VehicleGlobalPosition) {
        this.publisherVehicleGlobalPosition.publish(msg);
    }

    publishLocalPosition(msg: rclnodejs.px4_msgs.msg.VehicleLocalPosition) {
        this.publisherVehicleLocalPosition.publish(msg);
    }

    publishVechicleStatus(msg: rclnodejs.px4_msgs.msg.VehicleStatus) {
        this.publisherVehicleStatus.publish(msg);
    }

    publishBatteryStatus(msg: rclnodejs.px4_msgs.msg.BatteryStatus) {
        this.publisherBatteryStatus.publish(msg);
    }

    publishDebugValues(msg: string) {
        this.publisherDebugValues.publish({ data: msg });
    }

    publishStatus(msg: string) {
        this.publisherSatus.publish({ data: msg });
    }
}

function timestamp(): number {
    return Math.floor(new Date().getTime() / 1000);
}

export function createMissionResult(instanceCount: number, i: number, c: number, valid: boolean): rclnodejs.px4_msgs.msg.MissionResult {
    return {
        timestamp: timestamp(),
        instance_count: instanceCount,
        seq_reached: i,
        seq_current: Math.min(i + 1, c - 1),
        seq_total: c,
        valid: valid,
        warning: !valid,
        finished: i == c - 1,
        failure: false,
        stay_in_failsafe: false,
        flight_termination: false,
        item_do_jump_changed: false,
        item_changed_index: 0,
        item_do_jump_remaining: 0,
        execution_mode: 0,
    };
}

export function createGlobalPosition(lat: number, lon: number, alt: number): rclnodejs.px4_msgs.msg.VehicleGlobalPosition {
    return {
        timestamp: timestamp(),
        timestamp_sample: 0,
        lat: lat,
        lon: lon,
        alt: alt,
        alt_ellipsoid: 0,
        delta_alt: 0,
        lat_lon_reset_counter: 0,
        alt_reset_counter: 0,
        eph: 0,
        epv: 0,
        terrain_alt: 0,
        terrain_alt_valid: true,
        dead_reckoning: true,
    };
}

export function createLocalPosition(lat: number, lon: number, alt: number): rclnodejs.px4_msgs.msg.VehicleLocalPosition {
    return {
        timestamp: timestamp(),
        timestamp_sample: 0,
        xy_valid: true,
        z_valid: true,
        v_xy_valid: true,
        v_z_valid: true,
        x: lat,
        y: lon,
        z: alt,
        delta_xy: [0, 0],
        xy_reset_counter: 0,
        delta_z: 0,
        z_reset_counter: 0,
        vx: 0,
        vy: 0,
        vz: 0,
        z_deriv: 0,
        delta_vxy: [0, 0],
        vxy_reset_counter: 0,
        delta_vz: 0,
        vz_reset_counter: 0,
        ax: 0,
        ay: 0,
        az: 0,
        heading: 0,
        delta_heading: 0,
        heading_reset_counter: 0,
        xy_global: true,
        z_global: true,
        ref_timestamp: 0,
        ref_lat: 0,
        ref_lon: 0,
        ref_alt: 0,
        dist_bottom: 0,
        dist_bottom_valid: true,
        dist_bottom_sensor_bitfield: 0,
        eph: 0,
        epv: 0,
        evh: 0,
        evv: 0,
        vxy_max: 0,
        vz_max: 0,
        hagl_min: 0,
        hagl_max: 0,
    };
}

export function createVehicleStatus(armingState: number, navState: number): rclnodejs.px4_msgs.msg.VehicleStatus {
    return {
        timestamp: timestamp(),
        nav_state: navState,
        nav_state_timestamp: 0,
        arming_state: armingState,
        hil_state: 0,
        failsafe: false,
        failsafe_timestamp: 0,
        system_type: 0,
        system_id: 0,
        component_id: 0,
        vehicle_type: 0,
        is_vtol: false,
        is_vtol_tailsitter: false,
        vtol_fw_permanent_stab: false,
        in_transition_mode: false,
        in_transition_to_fw: false,
        rc_signal_lost: false,
        rc_input_mode: 0,
        data_link_lost: false,
        data_link_lost_counter: 0,
        high_latency_data_link_lost: false,
        engine_failure: false,
        mission_failure: false,
        failure_detector_status: 0,
        onboard_control_sensors_present: 0,
        onboard_control_sensors_enabled: 0,
        onboard_control_sensors_health: 0,
        latest_arming_reason: 0,
        latest_disarming_reason: 0,
        armed_time: 0,
        takeoff_time: 0,
    };
}

export function createBatteryStatus(): rclnodejs.px4_msgs.msg.BatteryStatus {
    return {
        timestamp: timestamp(),
        voltage_v: 12.0,
        voltage_filtered_v: 12.0,
        current_a: 1.25,
        current_filtered_a: 1.25,
        average_current_a: 1.25,
        discharged_mah: 1.0,
        remaining: 1.0,
        scale: 0,
        temperature: 25,
        cell_count: 7,
        connected: true,
        source: 0,
        priority: 0,
        capacity: 0,
        cycle_count: 10,
        run_time_to_empty: 100,
        average_time_to_empty: 100,
        serial_number: 123,
        manufacture_date: 2021,
        state_of_health: 1,
        max_error: 0,
        id: 123,
        interface_error: 0,
        voltage_cell_v: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
        max_cell_voltage_delta: 0,
        is_powering_off: false,
        warning: 0,
    };
}
