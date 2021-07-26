import { TestContext } from "./ctx";
import * as test from "tape";

test("single drone should complete task", async (t) => {
    const ctx = await TestContext.create();

    // Create drone
    const d1 = await ctx.createDrone("d1");
    t.pass("drone #1 created");
    
    await d1.telemetry();
    t.pass("drone #1 telemetry received");

    const posBegin = await d1.position();
    t.pass("drone #1 gps position received");

    const target = posBegin.move(10, 0);

    const mission = await ctx.createMission("m1");
    t.pass("mission created");

    await mission.assignDrone(d1);
    t.pass("drone #1 assigned to mission");

    await d1.joinedMission(mission);
    t.pass("drone #1 joined mission");

    const task = await mission.addFlyToTask(target);
    t.pass("task created");

    const taskCompleted = task.completed();
    const planCompleted = mission.planCompleted();
    const droneDisarmed = d1.disarmed();
    await taskCompleted;
    t.pass("task completed");
    await planCompleted;
    t.pass("mission plan completed");

    const posEnd = await d1.position();
    t.true(posEnd.distanceTo(target) < 0.5, "drone #1 reached target coordinates");

    await droneDisarmed;
    t.pass("drone #1 landed and disarmed");

    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");

    await ctx.removeMission(mission);
    t.pass("mission removed");

    ctx.close();
});
