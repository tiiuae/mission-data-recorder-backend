import { runF4F } from "./f4f";

async function run() {
    const deviceNames = process.argv.slice(2);

    if (deviceNames.length == 0) {
        console.log("Usage: ts-node f4f-emulator.ts d1 d2 d3 [--slow | --fast]");
    } else {
        var cleanup = await runF4F(deviceNames);

        process.on('SIGINT', function () {
            cleanup();
            process.exit();
        });
    }
}

run();
