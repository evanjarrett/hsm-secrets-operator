#!/busybox/sh
set -e

# Debug: Show user and USB device permissions for agent mode only
if [ "$1" = "--mode=agent" ]; then
    echo "Starting pcscd as user: $(id)"
    echo "Groups: $(groups)"
    echo "USB device permissions:"
    if [ -d /dev/bus/usb ]; then
        ls -la /dev/bus/usb/ | head -20
        echo "Checking for specific USB devices..."
        find /dev/bus/usb -type c -exec ls -la {} \; 2>/dev/null | grep -E "20a0|4230" || echo "No HSM devices found by vendor/product ID yet"
    else
        echo "ERROR: /dev/bus/usb not mounted"
        exit 1
    fi

    # Try to trigger udev rules for USB devices
    if command -v udevadm >/dev/null 2>&1; then
        echo "Triggering udev rules for USB devices..."
        udevadm trigger --subsystem-match=usb --action=add 2>/dev/null || true
        udevadm settle --timeout=2 2>/dev/null || true
    fi

    # Apply CCID interface fix for Pico HSM
    echo "Applying CCID interface fix for Pico HSM..."

    # Check if we can modify the CCID configuration
    CCID_CONFIG="/usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist"
    if [ -f "$CCID_CONFIG" ]; then
        # Create backup
        cp "$CCID_CONFIG" /tmp/Info.plist.backup

        echo "Original CCID driver options:"
        grep -A 1 "ifdDriverOptions" "$CCID_CONFIG" || echo "Not found"

        # Fix: Enable CCID Exchange option (0x01) for interface flexibility
        # This makes CCID more permissive about interface selection
        if grep -q "<string>0x0000</string>" "$CCID_CONFIG"; then
            sed -i 's/<string>0x0000<\/string>/<string>0x0001<\/string>/' "$CCID_CONFIG"
            echo "✅ Enabled DRIVER_OPTION_CCID_EXCHANGE_AUTHORIZED (0x01)"
        else
            echo "⚠️  CCID driver options already modified or not found"
        fi

        echo "Modified CCID driver options:"
        grep -A 1 "ifdDriverOptions" "$CCID_CONFIG" || echo "Not found"

        echo "CCID interface fix applied:"
        echo "- Pico HSM interface 0 should now be tried first"
        echo "- CCID will be more flexible about interface detection"
        echo "- Debug environment variables: LIBCCID_ifdLogLevel=$LIBCCID_ifdLogLevel"
    else
        echo "❌ CCID Info.plist not found at $CCID_CONFIG"
        echo "Falling back to environment variables only"
    fi

    # Start pcscd with debug output
    echo "Starting pcscd..."
    pcscd -f -d -a &
    PCSCD_PID=$!

    sleep 3

    # Verify pcscd started successfully
    if ! kill -0 $PCSCD_PID 2>/dev/null; then
        echo "ERROR: pcscd failed to start"
        echo "Checking USB access permissions..."
        # Try to access a USB device to see the actual error
        cat /dev/bus/usb/001/001 > /dev/null 2>&1 || echo "Cannot read USB devices: $?"
        exit 1
    fi

    echo "pcscd started successfully with PID $PCSCD_PID"
fi

# Entrypoint script for HSM Secrets Operator
# Supports running manager, discovery, or agent binaries from the same container

case "$1" in
    "--mode="*)
        # Direct mode flag usage (preferred)
        exec /hsm-operator "$@"
        ;;
    *)
        # Default to manager for backward compatibility
        exec /hsm-operator --mode=manager "$@"
        ;;
esac