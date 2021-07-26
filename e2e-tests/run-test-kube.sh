#!/bin/sh

export SACP_BACKEND_BASE_URL=http://web-backend-svc:8083
export SACP_MISSION_CONTROL_BASE_URL=http://mission-control-svc:8082

node build/$1*.js