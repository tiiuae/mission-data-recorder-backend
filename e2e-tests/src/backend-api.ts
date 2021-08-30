import { Observable, merge } from "rxjs";
import * as WebSocket from "websocket";
import fetch from "node-fetch";
import * as gps from "./gps";

const webBackendBaseUrl = process.env.SACP_BACKEND_BASE_URL != undefined ? process.env.SACP_BACKEND_BASE_URL : "http://127.0.0.1:8083";
const missionControlBaseUrl = process.env.SACP_MISSION_CONTROL_BASE_URL != undefined ? process.env.SACP_MISSION_CONTROL_BASE_URL : "http://127.0.0.1:8082";

function kubifyUrl(url: string, simulationName: string): string {
    return url.replace("mission-control-svc", `mission-control-svc.${simulationName}`).replace("web-backend-svc", `web-backend-svc.${simulationName}`);
}

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

export async function subscribeAll(simulationName: string): Promise<Observable<Message>> {
    const o1 = await subscribeWebSocket(kubifyUrl(webBackendBaseUrl, simulationName) + "/subscribe");
    const o2 = await subscribeWebSocket(kubifyUrl(missionControlBaseUrl, simulationName) + "/subscribe");

    return merge(o1, o2);
}

// ================================================================================
// HTTP API
// ================================================================================

export async function waitForStartup(simulationName: string): Promise<boolean> {
    const h1 = checkHealthz(kubifyUrl(webBackendBaseUrl, simulationName) + "/healthz");
    const h2 = checkHealthz(kubifyUrl(missionControlBaseUrl, simulationName) + "/healthz");

    const r1 = await h1;
    const r2 = await h2;

    return r1 && r2;
}

async function sleep(interval: number): Promise<void> {
    return new Promise(resolve => setTimeout(() => resolve(), interval));
}

async function checkHealthz(url: string): Promise<boolean> {
    for (var i = 0; i < 20; i++) {
        try {
            const res = await getHealthz(url);
            if (res.status == 200) {
                return true;
            }
        }
        catch (e) {
        }

        await sleep(1000);
    }

    return false;
}

async function getHealthz(url: string) {
    return fetch(url, {
        method: "GET",
    });
}

export async function createMission(simulationName: string, missionSlug: string) {
    return fetch(`${kubifyUrl(missionControlBaseUrl, simulationName)}/missions`, {
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

export async function assignDrone(simulationName: string, missionSlug: string, droneId: string) {
    return fetch(`${kubifyUrl(missionControlBaseUrl, simulationName)}/missions/${missionSlug}/drones`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            device_id: droneId,
        }),
    });
}

export async function removeDrone(simulationName: string, missionSlug: string, droneId: string) {
    return fetch(
        `${kubifyUrl(missionControlBaseUrl, simulationName)}/missions/${missionSlug}/drones/${droneId}`,
        {
            method: "DELETE",
        }
    );
}

export async function addFlyToTask(simulationName: string, missionSlug: string, taskId: string, target: gps.Point) {
    return fetch(`${kubifyUrl(missionControlBaseUrl, simulationName)}/missions/${missionSlug}/backlog`, {
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
                alt: 1.0,
            },
        }),
    });
}

export async function addPredefinedTask(
    simulationName: string,
    missionSlug: string,
    taskId: string,
    droneId: string
) {
    return fetch(`${kubifyUrl(missionControlBaseUrl, simulationName)}/missions/${missionSlug}/backlog`, {
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

export async function deleteMission(simulationName: string, missionSlug: string) {
    return fetch(`${kubifyUrl(missionControlBaseUrl, simulationName)}/missions/${missionSlug}`, {
        method: "DELETE",
        headers: {
            "Content-Type": "application/json",
        },
    });
}
