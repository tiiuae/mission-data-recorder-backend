import * as geolib from "geolib";

export class Point {
    private geolibStruct: any;

    constructor(public lat: number, public lon: number) {
        this.geolibStruct = { latitude: this.lat, longitude: this.lon };
    }

    public move(distanceMeters: number, bearingDegrees: number): Point {
        const dest = geolib.computeDestinationPoint(this.geolibStruct, distanceMeters, bearingDegrees);

        return new Point(dest.latitude, dest.longitude);
    }

    public distanceTo(other: Point): number {
        return geolib.getDistance(this.geolibStruct, other.geolibStruct);
    }

    public bearingTo(other: Point): number {
        return geolib.getRhumbLineBearing(this.geolibStruct, other.geolibStruct);
    }

    public between(other: Point): Point {
        const bearing = this.bearingTo(other);
        const distance = this.distanceTo(other);
        return this.move(distance / 2, bearing);
    }
}
