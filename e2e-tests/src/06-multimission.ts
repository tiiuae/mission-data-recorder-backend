import { TestContext } from "./ctx";
import * as test from "tape";

test("single drone should complete two missions with single task each", async (t) => {
    t.test("mission #1", async tc => {
        await testMission(tc, "m1");
    });
    t.test("mission #2", async tc => {
        await testMission(tc, "m2");
    });
});

async function testMission(t: test.Test, missionSlug: string): Promise<void> {
    const ctx = await TestContext.create();

    const d1 = await ctx.createDrone("d1");
    t.pass("drone #1 created");
    const posBegin = await d1.position();
    t.pass("drone #1 is online");

    const target = posBegin.move(randomBetween(5, 10), randomBetween(0, 360));

    const mission = await ctx.createMission(missionSlug);
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
}

function randomBetween(min: number, max: number) {
    return Math.floor(Math.random() * (max - min + 1) + min);
}
