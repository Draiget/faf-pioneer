#!/bin/bash

# Get the user ID from the script argument.
USER_ID=$1
# Define the base port.
GPGNET_BASE_PORT=21000
# Define API root or use existing exported env-var.
API_ROOT=${API_ROOT:-http://localhost:8080}

if [ "$USER_ID" == "1" ]; then
    read -r -d '' USED_TOKEN <<'EOF'
eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIiwiZXh0Ijp7InJvbGVzIjpbIlVTRVIiXSwiZ2FtZUlkIjoxMDB9LCJzY3AiOlsibG9iYn
kiXSwiaXNzIjoiaHR0cHM6Ly9pY2UuZmFmb3JldmVyLmNvbSIsImF1ZCI6Imh0dHBzOi8vaWNlLmZhZm9yZXZlci5jb20iLCJleHAiOjIwMDAwMDAwMDAsI
mlhdCI6MTc0MTAwMDAwMCwianRpIjoiMDE5YjBmMDYtOGJlYi00NzEyLWFiNWUtNGUyNmVjMTM0YjFlIn0.CHEtH0I-BacvjIc_a8ZSKcXMmRZqObGIqScs
8BNbZrcje9GVvnTeJEkOxh3Lpo0C1Cm8_x_YQ-zilMTmVu87ZH31_FRYvJuaU9gjo3izmHcncWmSOpjg2n8BtkPXcnggdxM5DW7bPUytkgPGhvFUbeTNRw0
Lv1Atb9L2NcW33jhQ-jz-3Ev0fVfgAzJMxrhDCpoCw4QMk6doEIbmJ0Egl1-9AHyr3jd1PXMQAI2K3dX2v0hUmOJ2MxClukUFXkXRp76ZJ9L594YU1gLlIp
rcuPtRQCIvgJ_gD2Cd6iPQHAUFFvNFmpyLVDU3fgrznWIRkcu2CWSlybhFHCvx5Eldhg
EOF
elif [ "$USER_ID" == "2" ]; then
    read -r -d '' USED_TOKEN <<'EOF'
eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIyIiwiZXh0Ijp7InJvbGVzIjpbIlVTRVIiXSwiZ2FtZUlkIjoxMDB9LCJzY3AiOlsibG9iYn
kiXSwiaXNzIjoiaHR0cHM6Ly9pY2UuZmFmb3JldmVyLmNvbSIsImF1ZCI6Imh0dHBzOi8vaWNlLmZhZm9yZXZlci5jb20iLCJleHAiOjIwMDAwMDAwMDAsI
mlhdCI6MTc0MTAwMDAwMCwianRpIjoiZmNkOTkwZjYtNWU3Mi00MjA4LTg1MzktNmQ1NDU3NDkyOTY4In0.Ef0LJlcziNPaq2OHSUXaHWvDMt6hx42qa9IU
DdAUmReluGz0XNPmpJqtbXk2b5uoAZ2s1dS8DQ0axtfjtKRgH9sElnZW1uFahNOYNM8rDYKE9gw2oXUkJQwg-orvOUaSZncX0YNehtJmSzYA7PpbhLiJ9yv
roamQ2XqjfcOZ15iz0hsLJ5HM8kB0x09zVDQdncelaqatVLMRRL1xm7PZyavp39yca8kvuk98_IylJtDi0SfkShx-fRoKMBDu9bwqgv8ldpIkN6-x6yuSv_
Clo8i7ct7Np8lDhUFDv3mQsbCgEt5FUYTSXplxTO84R7dnwUfFNfVn7qrQqxhpsij9NQ
EOF
else
    echo "Invalid USER_ID"
    exit 1
fi

# Calculate the USED_PORT by adding USER_ID to BASE_PORT.
GPGNET_PORT=$((GPGNET_BASE_PORT + USER_ID))

go run main.go \
  --user-id "${USER_ID}" \
  --game-id 100 \
  --gpgnet-port ${GPGNET_PORT} \
  --api-root "${API_ROOT}" \
  --access-token="${USED_TOKEN}"
