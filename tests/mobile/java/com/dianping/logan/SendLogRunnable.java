package com.dianping.logan;
import java.io.File;
// Only Android scheduling is replaced, not the HTTP sender.
public abstract class SendLogRunnable {
    public abstract void sendLog(File file);
    protected void finish() {}
}
