import { TestContext } from "./ctx";
import * as test from "tape";

test("task should fail if drone unable to execute", async (t) => {
    const ctx = await TestContext.create();
    t.pass("simulation created");

    const d1 = await ctx.createDrone();
    t.pass("drone #1 created");
    
    const pos = await d1.position();
    t.pass("drone #1 telemetry received");

    // Target too far
    const target = pos.move(1000, 90);
    
    const mission = await ctx.createMission("m1");
    t.pass("mission created");

    await mission.assignDrone(d1);
    t.pass("drone #1 assigned to mission");

    await d1.joinedMission(mission);
    t.pass("drone #1 joined mission");

    const task = await mission.addFlyToTask(target);
    t.pass("task created");

    await task.failed();
    t.pass("task failed");

    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");

    await ctx.removeMission(mission);
    t.pass("mission removed");

    await ctx.close();
});