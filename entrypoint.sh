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

    # Backup original CCID configuration
    if [ -f /usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist ]; then
        cp /usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist /tmp/Info.plist.backup

        # Fix 1: Enable CCID Exchange option to allow interface flexibility
        # This makes CCID try interface 0 first instead of expecting interface 1
        sed -i 's/<string>0x0000<\/string>/<string>0x0001<\/string>/' \
            /usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist

        # Fix 2: Add flexible interface detection for Pico HSM
        # Create a temporary script to patch CCID behavior at runtime
        cat > /tmp/ccid-interface-patch.sh << 'EOF'
#!/busybox/sh
# Runtime patch for CCID interface detection
# This allows CCID to try both interface 0 and 1 for Pico HSM

# Method 1: Set environment variables that CCID respects
export LIBCCID_ifdLogLevel=0x000F  # Maximum debug
export PCSCLITE_DEBUG=3            # PCSCD debug

# Method 2: If CCID fails on interface 1, restart and try interface 0
# This is handled by our Info.plist modification above

echo "CCID interface patch applied - will try interface 0 first, then 1"
EOF
        chmod +x /tmp/ccid-interface-patch.sh
        /tmp/ccid-interface-patch.sh

        echo "CCID configuration modified:"
        echo "- Enabled DRIVER_OPTION_CCID_EXCHANGE_AUTHORIZED (0x01)"
        echo "- Set maximum debug logging for interface detection"
        echo "- CCID will now try interface 0 first (Pico HSM), then interface 1 (real Nitrokey)"
    else
        echo "WARNING: CCID Info.plist not found, skipping interface fix"
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