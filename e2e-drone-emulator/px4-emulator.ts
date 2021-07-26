import { runPX4 } from "./px4";

async function run() {
    const deviceNames = process.argv.slice(2);

    if (deviceNames.length == 0) {
        console.log("Usage: ts-node px4-emulator.ts d1 d2 d3 [--slow | --fast]");
    } else {
        var cleanup = await runPX4(deviceNames);

        process.on('SIGINT', function () {
            cleanup();
            process.exit();
        });
    }
}

run();
