import { TestContext } from "./ctx";
import * as test from "tape";

test("single drone should complete multiple tasks assigned in parallel", async (t) => {
    const ctx = await TestContext.create();
    t.pass("simulation created");

    const d1 = await ctx.createDrone();
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

    const halfway1 = d1.isNear(target1, 5);
    const halfway2 = d1.isNear(target2, 5);

    const task1 = await mission.addFlyToTask(target1);
    t.pass("task #1 created");

    await halfway1;
    t.pass("halfway to #1 reached");

    const task2 = await mission.addFlyToTask(target2);
    t.pass("task #2 created");
    const task2completed = task2.completed();
    
    await task1.completed();
    t.pass("target #1 reached");

    await halfway2;
    t.pass("halfway to #2 reached");

    const task3 = await mission.addFlyToTask(target3);
    t.pass("task #3 created");
    const task3completed = task3.completed();
    const task4 = await mission.addFlyToTask(target4);
    t.pass("task #4 created");
    const task4completed = task4.completed();

    await task2completed;
    t.pass("target #2 reached");
    await task3completed;
    t.pass("target #3 reached");
    await task4completed;
    t.pass("target #4 reached");

    await d1.disarmed();
    t.pass("drone #1 landed and disarmed");

    await mission.removeDrone(d1);
    t.pass("drone #1 removed from mission");

    await ctx.removeMission(mission);
    t.pass("mission removed");

    await ctx.close();
});