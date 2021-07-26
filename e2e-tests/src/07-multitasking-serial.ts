import { TestContext } from "./ctx";
import * as test from "tape";

test("single drone should complete multiple tasks assigned in serial", async (t) => {
    const ctx = await TestContext.create();

    const d1 = await ctx.createDrone("d1");
    t.pass("drone #1 created");
    const posBegin = await d1.position();
    t.pass("drone #1 is online");

    // Fly rectangular path (10x10 meters), return to starting position
    const target4 = posBegin;
    const target1 = target4.move(10, 0);
    const target2 = target1.move(10, 90);
    const target3 = target2.move(10, 180);

    const mission = await ctx.createMission("m1");
    t.pass("mission created");

    await mission.assignDrone(d1);
    t.pass("drone #1 assigned to mission");

    await d1.joinedMission(mission);
    t.pass("drone #1 joined mission");

    const task1 = await mission.addFlyToTask(target1);
    t.pass("task #1 created");
    await task1.completed();
    t.pass("target #1 reached");

    const task2 = await mission.addFlyToTask(target2);
    t.pass("task #2 created");
    await task2.completed();
    t.pass("target #2 reached");

    const task3 = await mission.addFlyToTask(target3);
    t.pass("task #3 created");
    await task3.completed();
    t.pass("target #3 reached");
    
    const task4 = await mission.addFlyToTask(target4);
    t.pass("task #4 created");
    await task4.completed();
    t.pass("target #4 reached");

    await d1.disarmed();
    t.pass("drone #1 landed and disarmed");

    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");

    await ctx.removeMission(mission);
    t.pass("mission removed");

    ctx.close();
});
