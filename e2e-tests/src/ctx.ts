import { Observable, Subject } from "rxjs";
import { filter, take, timeout, toArray, publish, refCount, takeUntil } from "rxjs/operators";
import { ConnectableObservable } from "rxjs/index";
import * as api from "./backend-api";
import * as events from "./events";
import * as gps from "./gps";

export class TestContext {
    private constructor(private events: Observable<any>, private quit: Subject<void>) {
    }

    async createMission(missionSlug: string): Promise<Mission> {
        const res = await api.createMission(missionSlug);
        if (res.status > 299) {
            throw new Error(`failed to create mission '${missionSlug}' (${res.status} ${res.statusText})`);
        }
        return new Mission(missionSlug, this.events);
    }

    async removeMission(mission: Mission): Promise<void> {
        const res = await api.deleteMission(mission.slug);
        if (res.status > 299) {
            throw new Error(`failed to remove mission '${mission.slug}' (${res.status} ${res.statusText})`);
        }
    }

    async createDrone(droneId: string): Promise<Drone> {
        return Promise.resolve(new Drone(droneId, this.events));
    }
    
    close() {
        this.quit.next();
    }

    static async create(): Promise<TestContext> {
        const quit = new Subject<void>();
        const o = await api.subscribeAll();
        
        const events = o.pipe(takeUntil(quit), publish()) as ConnectableObservable<any>;
        events.connect();

        return Promise.resolve(new TestContext(events, quit));
    }
}

export class Mission {
    private taskId: number = 0;

    constructor(public slug: string, private events: Observable<any>) {}

    async assignDrone(drone: Drone): Promise<void> {
        const res = await api.assignDrone(this.slug, drone.id)
        if (res.status > 299) {
            throw new Error(`failed to assign drone '${drone.id}' to mission '${this.slug}' (${res.status} ${res.statusText})`);
        }
    }

    async removeDrone(drone: Drone): Promise<void> {
        const res = await api.removeDrone(this.slug, drone.id)
        if (res.status > 299) {
            throw new Error(`failed to remove drone '${drone.id}' to mission '${this.slug}' (${res.status} ${res.statusText})`);
        }
    }
    
    async addFlyToTask(target: gps.Point): Promise<Task> {
        const tid = this.generateTaskId();
        const res = await api.addFlyToTask(this.slug,tid, target);
        if (res.status > 299) {
            throw new Error(`failed to add task to '${this.slug}' (${res.status} ${res.statusText})`);
        }

        return new Task(tid, this.slug, this.events);
    }

    async addPredefinedTask(drone: Drone): Promise<Task> {
        const tid = this.generateTaskId();
        const res = await api.addPredefinedTask(this.slug, tid, drone.id);
        if (res.status > 299) {
            throw new Error(`failed to add task to '${this.slug}' (${res.status} ${res.statusText})`);
        }

        return new Task(tid, this.slug, this.events);
    }

    async planCompleted(): Promise<void> {
        return events.first(this.events, x => events.isMissionPlanCompleted(x, this.slug));
    }

    async isSomeNear(point: gps.Point, distanceMeters: number) {
        return events.isSomeNear(this.events, point, distanceMeters);
    }

    private generateTaskId(): string {
        this.taskId++;
        return `task-${this.taskId}`;
    }
}

export class Drone {
    constructor(public id: string, private events: Observable<any>) {}

    async telemetry(): Promise<api.TelemetryEvent> {
        return events.currentTelemetry(this.events, this.id);
    }

    async position(): Promise<gps.Point> {
        const tel = await events.currentTelemetry(this.events, this.id);
        return new gps.Point(tel.lat, tel.lon);
    }

    async joinedMission(mission: Mission): Promise<void> {
        return events.first(this.events, x => events.isJoinedMission(x, mission.slug, this.id));
    }

    async disarmed(): Promise<void> {
        return events.first(this.events, x => x.event == "telemetry" && x.device == this.id && x.payload.arming_state == "Standby");
    }

    async isNear(point: gps.Point, distanceMeters: number) {
        return events.isNear(this.events, this.id, point, distanceMeters);
    }

    async debugValues(): Promise<any> {
        return events.first(this.events, x => x.event == "debug-values" && x.device == this.id);
    }
}

export class Task {
    constructor(public id: string, private missionSlug: string, private events: Observable<any>) {}

    async completed(): Promise<any> {
        return events.first(this.events, x => events.isTaskCompleted(x, this.missionSlug, this.id));
    }

    async failed(): Promise<any> {
        return events.first(this.events, x => events.isTaskFailed(x, this.missionSlug, this.id));
    }
}