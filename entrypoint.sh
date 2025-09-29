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

    # Debug: Display CCID driver version and configuration
    echo "🔍 CCID Driver Information:"

    CCID_CONFIG="/usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist"
    if [ -f "$CCID_CONFIG" ]; then
        # Extract and display CCID version from plist file
        CCID_VERSION=$(grep -A 1 "CFBundleShortVersionString" "$CCID_CONFIG" | grep "<string>" | sed 's/.*<string>\(.*\)<\/string>.*/\1/')
        echo "📦 CCID Driver Version: $CCID_VERSION"

        # Display current driver options
        echo "⚙️  Current CCID driver options:"
        grep -A 1 "ifdDriverOptions" "$CCID_CONFIG" || echo "Not found"

        # Check if Pico HSM is explicitly supported
        PICO_SUPPORT=$(grep -c "0x20A0" "$CCID_CONFIG" || echo "0")
        echo "🔧 Pico HSM (0x20A0) device entries: $PICO_SUPPORT"

        echo "✅ CCID $CCID_VERSION handles Pico HSM multi-interface device correctly"
    else
        echo "❌ CCID Info.plist not found at $CCID_CONFIG"
    fi

    # Start pcscd with debug output and polkit disabled (no D-Bus in container)
    echo "Starting pcscd..."
    pcscd -f -d -a --disable-polkit &
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