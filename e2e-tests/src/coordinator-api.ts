
import fetch from "node-fetch";

const coordinatorBaseUrl = process.env.SACP_COORDINATOR_BASE_URL != undefined ? process.env.SACP_COORDINATOR_BASE_URL : "http://127.0.0.1:80";

export async function createSimulation(simulationSlug: string): Promise<any> {
    return fetch(`${coordinatorBaseUrl}/simulations`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            name: simulationSlug,
            world: "empty.world",
            standalone: true,
            data_image: "ghcr.io/tiiuae/tii-gazebo-data:dev",
            mission_data_directory: "/minikube-host"
        }),
    });
}

export async function removeSimulation(simulationSlug: string): Promise<any> {
    return fetch(`${coordinatorBaseUrl}/simulations/${simulationSlug}`, {
        method: "DELETE",
    });
}

export async function addDrone(simulationSlug: string, deviceName: string, x: number, y: number): Promise<any> {
    return fetch(`${coordinatorBaseUrl}/simulations/${simulationSlug}/drones`, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
        },
        body: JSON.stringify({
            drone_id: deviceName,
            private_key: "",
            pos_x: x,
            pos_y: y,
            pos_z: 0.0,
            yaw: 0.0,
            pitch: 0.0,
            roll: 0.0,
            location: "cluster",
            record_topics: [],
            record_size_threshold: 52428800
        }),
    });
}
