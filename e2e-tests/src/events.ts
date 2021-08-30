import { Observable } from "rxjs";
import { filter, take, timeout, toArray, tap, map } from "rxjs/operators";
import { Message, TelemetryEvent } from "./backend-api";
import * as gps from "./gps";

// const timeoutMs = 30000;
const timeoutMs = 300000;

// Returns first event that matches a given predicate
export function first(
    events: Observable<any>,
    predicate: (value: any) => boolean
): Promise<any> {
    return events
        .pipe(
            filter((x: any) => predicate(x)),
            take(1),
            timeout(timeoutMs)
        )
        .toPromise();
}

export function currentTelemetry(
    events: Observable<Message>,
    droneId: string
): Promise<TelemetryEvent> {
    return events
        .pipe(
            filter((x) => x.event == "telemetry" && x.device == droneId && x.payload.lat != 0 && x.payload.lon != 0),
            map(x => x.event == "telemetry" ? x.payload : null),
            take(1),
            timeout(timeoutMs)
        )
        .toPromise();
}

export function isNear(
    events: Observable<Message>,
    droneId: string,
    point: gps.Point,
    distance: number
): Promise<void> {
    return events
        .pipe(
            filter(
                (x) =>
                    x.event == "telemetry" &&
                    x.device == droneId &&
                    new gps.Point(x.payload.lat, x.payload.lon).distanceTo(
                        point
                    ) <= distance
            ),
            map(_ => null),
            take(1),
            timeout(timeoutMs)
        )
        .toPromise();
}

export function isSomeNear(
    events: Observable<Message>,
    point: gps.Point,
    distance: number
): Promise<void> {
    return events
        .pipe(
            filter(
                (x) =>
                    x.event == "telemetry" &&
                    new gps.Point(x.payload.lat, x.payload.lon).distanceTo(
                        point
                    ) <= distance
            ),
            map(_ => null),
            take(1),
            timeout(timeoutMs)
        )
        .toPromise();
}

// Match events

export function isMissionPlanCompleted(
    event: Message,
    missionSlug: string
): boolean {
    return (
        event.event == "mission-plan" &&
        event.mission_slug == missionSlug &&
        event.plan.every((x: any) => x.status == "completed")
    );
}

export function isTaskCompleted(
    event: Message,
    missionSlug: string,
    taskId: string
): boolean {
    return (
        event.event == "mission-plan" &&
        event.mission_slug == missionSlug &&
        event.plan.some((x: any) => x.id == taskId && x.status == "completed")
    );
}

export function isTaskFailed(
    event: Message,
    missionSlug: string,
    taskId: string
): boolean {
    return (
        event.event == "mission-plan" &&
        event.mission_slug == missionSlug &&
        event.plan.some((x: any) => x.id == taskId && x.status == "failed")
    );
}

export function isJoinedMission(
    event: Message,
    missionSlug: string,
    droneId: string
): boolean {
    return (
        event.event == "mission-drone-joined" &&
        event.mission_slug == missionSlug &&
        event.drone_id == droneId
    );
}