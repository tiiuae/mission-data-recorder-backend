import { TestContext } from "./ctx";
import * as gps from "./gps";
import * as test from "tape";
import * as fs from "fs";

test("two drones should complete one predefined task each", async (t) => {
    const ctx = await TestContext.create();
    t.pass("simulation created");

    // Create drones
    const d1 = await ctx.createDroneAt(0,0);
    t.pass("drone #1 created");
    const d2 = await ctx.createDroneAt(1,0);
    t.pass("drone #2 created");
    
    // Wait for drones to come online
    const start1 = await d1.position();
    t.pass("drone #1 is online");
    const start2 = await d2.position();
    t.pass("drone #2 is online");

    const center = start1.between(start2);
    const path1 = [ center.move(10, 0), center.move(15, 45), center.move(10, 90), start1 ];
    const path2 = [ center.move(10, 180), center.move(15, 225), center.move(10, 270), start2 ];

    // HACK: Write path files
    createPath(d1.id, path1);
    createPath(d2.id, path2);

    // Create mission, assign drone
    const mission = await ctx.createMission("m1");
    t.pass("mission created");

    const missionCompletedEvent = mission.planCompleted();
    const droneJoined1 = d1.joinedMission(mission);
    const droneJoined2 =  d2.joinedMission(mission);

    await mission.assignDrone(d1);
    t.pass("drone #1 assigned to mission");
    await mission.assignDrone(d2);
    t.pass("drone #2 assigned to mission");

    await droneJoined1;
    t.pass("drone #1 joined mission");
    await droneJoined2;
    t.pass("drone #2 joined mission");
    
    // Create task
    const t1 = await mission.addPredefinedTask(d1);
    t.pass("task #1 added");
    const t2 = await mission.addPredefinedTask(d2);
    t.pass("task #2 added");

    // Wait for mission completion
    await missionCompletedEvent;

    // Check coordinates
    const end1 = await d1.position();
    t.true(end1.distanceTo(start1) < 0.5, "drone #1 reached target coordinates");
    const end2 = await d2.position();
    t.true(end2.distanceTo(start2) < 0.5, "drone #1 reached target coordinates");

    // Cleanup
    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");
    await mission.removeDrone(d2);
    t.pass("drone #2 removed from mission");
    
    await ctx.removeMission(mission);
    t.pass("mission removed");

    await ctx.close();
});

function createPath(droneId: string, points: gps.Point[]) {
    const filename = `../../communication_link/missionengine/cmd/flightpath-${droneId}.json`;
    const json = JSON.stringify(points, null, 4);
    fs.writeFileSync(filename, json);
}
