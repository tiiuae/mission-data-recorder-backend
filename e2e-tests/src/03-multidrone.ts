import { TestContext } from "./ctx";
import * as test from "tape";

test("two drones should complete one task each", async (t) => {
    const ctx = await TestContext.create();

    // Create drones
    const d1 = await ctx.createDrone("d1");
    t.pass("drone #1 created");
    const d2 = await ctx.createDrone("d2");
    t.pass("drone #2 created");
    
    // Wait for drones to come online
    const pos1 = await d1.position();
    t.pass("drone #1 is online");
    const pos2 = await d2.position();
    t.pass("drone #2 is online");

    // Create target coordinates
    const center = pos1.between(pos2);
    const target1 = center.move(10, 0);
    const target2 = center.move(10, 180);

    // Create mission, assign drones
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

    // Create tasks
    const t1 = await mission.addFlyToTask(target1);
    const t1completed = t1.completed();
    t.pass("task #1 added");
    const t2 = await mission.addFlyToTask(target2);
    const t2completed = t2.completed();
    t.pass("task #2 added");
   
    // Wait for completion
    await t1completed;
    t.pass("task #1 completed");
    await t2completed;
    t.pass("task #2 completed");

    await missionCompletedEvent;
    t.pass("mission plan completed");

    // Cleanup
    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");
    await mission.removeDrone(d2);
    t.pass("drone #2 removed from mission");
    
    await ctx.removeMission(mission);
    t.pass("mission removed");

    ctx.close();
});