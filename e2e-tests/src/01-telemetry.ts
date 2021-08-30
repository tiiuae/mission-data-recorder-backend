import { TestContext } from "./ctx";
import * as test from "tape";

test("should receive telemetry", async (t) => {
    const ctx = await TestContext.create();
    t.pass("simulation created");

    const d1 = await ctx.createDrone();
    const telemetry = await d1.telemetry();

    t.pass("telemetry event received");
    t.equal(telemetry.battery_voltage, 12, "correct battery voltage");
    t.equal(telemetry.navigation_mode, "Auto loiter mode", "correct navigation mode");

    await ctx.close();
});

test("should receive debug values", async (t) => {
    const ctx = await TestContext.create();
    const d1 = await ctx.createDrone();
    const result = await d1.debugValues();
    
    t.pass("debug values event received");
    t.equal(result.payload[0].key, "f4femu:text", "correct key");
    t.equal(result.payload[0].value, "hello world", "correct value");

    await ctx.close();
});