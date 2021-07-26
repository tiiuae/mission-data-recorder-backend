import { Observable, merge } from "rxjs";
import * as WebSocket from "websocket";
import fetch from "node-fetch";
import * as gps from "./gps";

const webBackendBaseUrl = process.env.SACP_BACKEND_BASE_URL != undefined ? process.env.SACP_BACKEND_BASE_URL : "http://127.0.0.1:8083";
const missionControlBaseUrl = process.env.SACP_MISSION_CONTROL_BASE_URL != undefined ? process.env.SACP_MISSION_CONTROL_BASE_URL : "http://127.0.0.1:8082";

// ================================================================================
// WebSocket API
// ================================================================================

export interface TelemetryEvent {
    lat: number;
    lon: number;
    arming_state: string;
    navigation_mode: string;
    battery_voltage: number;
}

export interface DebugEvent {
    from: string;
    title: string;
    message: string;
}

export interface DebugValue {
    key: string;
    value: string;
    updated: string;
}

export interface BacklogItemStatus {
    id: string;
    assigned_to: string;
    status: string;
}

export interface WaypointStatus {
    reached: boolean;
    lat: number;
    lon: number;
    alt: number;
}

export interface FlyToPayload {
    lat: number;
    lon: number;
    alt: number;
}

export interface PredefinedPayload {
    drone: string;
}

export type Message =
    | { event: "telemetry", device: string, payload: TelemetryEvent }
    | { event: "debug-event", device: string, payload: DebugEvent }
    | { event: "debug-values", device: string, payload: DebugValue[] }
    | { event: "mission-created", mission_slug: string, misison_name: string }
    | { event: "mission-removed", mission_slug: string }
    | { event: "mission-drone-assigned", mission_slug: string, drone_id: string }
    | { event: "mission-drone-removed", mission_slug: string, drone_id: string }
    | { event: "mission-drone-got-trusted", mission_slug: string, drone_id: string }
    | { event: "mission-drone-joined", mission_slug: string, drone_id: string }
    | { event: "mission-drone-left", mission_slug: string, drone_id: string }
    | { event: "mission-drone-failed", mission_slug: string, drone_id: string }
    | { event: "mission-backlog-item-added", mission_slug: string, item_id: string, item_type: "fly-to", item_priority: number, item_payload: FlyToPayload }
    | { event: "mission-backlog-item-added", mission_slug: string, item_id: string, item_type: "execute-preplanned", item_priority: number, item_payload: PredefinedPayload }
    | { event: "mission-plan", mission_slug: string, drone_id: string, plan: BacklogItemStatus[] }
    | { event: "flight-plan", mission_slug: string, drone_id: string, path: WaypointStatus[] }

async function subscribeWebSocket(url: string): Promise<Observable<any>> {
    const client = new WebSocket.client();

    const con = await new Promise<WebSocket.connection>((resolve, reject) => {
        client.on("connect", (con) => {
            resolve(con);
        });
        client.on("connectFailed", (err) => {
            reject(err);
        });
        client.connect(url);
    });

    return new Observable(result => {
        con.on("message", (msg) => {
            if (msg.type == "utf8") {
                let data: any = JSON.parse(msg.utf8Data);
                result.next(data);
            }
        });
        con.on("error", (err) => {
            result.error(err);
            result.complete();
        });
        con.on("close", () => {
            result.complete();
        });

        return () => {
            con.close();
            con.socket.destroy();
            client.abort();
            result.complete();
        }
    });
}

export async function subscribeAll(): Promise<Observable<Message>> {
    const o1 = await subscribeWebSocket(webBackendBaseUrl + "/subscribe");
    const o2 = await subscribeWebSocket(missionControlBaseUrl + "/subscribe");

    return merge(o1, o2);
}

// ================================================================================
// HTTP API
// ================================================================================

export async function createMission(missionSlug: string) {
    return fetch(`${missionControlBaseUrl}/missions`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            slug: missionSlug,
            name: `${missionSlug}-mission`,
        }),
    });
}

export async function assignDrone(missionSlug: string, droneId: string) {
    return fetch(`${missionControlBaseUrl}/missions/${missionSlug}/drones`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            device_id: droneId,
        }),
    });
}

export async function removeDrone(missionSlug: string, droneId: string) {
    return fetch(
        `${missionControlBaseUrl}/missions/${missionSlug}/drones/${droneId}`,
        {
            method: "DELETE",
        }
    );
}

export async function addFlyToTask(missionSlug: string, taskId: string, target: gps.Point) {
    return fetch(`${missionControlBaseUrl}/missions/${missionSlug}/backlog`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            id: taskId,
            type: "fly-to",
            priority: 1,
            payload: {
                lat: target.lat,
                lon: target.lon,
                alt: 10.0,
            },
        }),
    });
}

export async function addPredefinedTask(
    missionSlug: string,
    taskId: string,
    droneId: string
) {
    return fetch(`${missionControlBaseUrl}/missions/${missionSlug}/backlog`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            id: taskId,
            type: "execute-preplanned",
            priority: 1,
            payload: {
                drone: droneId,
            },
        }),
    });
}

export async function deleteMission(missionSlug: string) {
    return fetch(`${missionControlBaseUrl}/missions/${missionSlug}`, {
        method: "DELETE",
        headers: {
            "Content-Type": "application/json",
        },
    });
}
