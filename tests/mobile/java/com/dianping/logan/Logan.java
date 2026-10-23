package com.dianping.logan;
import java.io.File;
import java.util.Map;
// API bridge double: actual sender implementation is compiled alongside this file.
public final class Logan {
    public static File fixture;
    public static void s(String url, String date, Map<String,String> headers, SendLogCallback callback) {
        SendLogDefaultRunnable sender = new SendLogDefaultRunnable();
        sender.setUrl(url);
        sender.setRequestHeader(headers);
        sender.setSendLogCallback(callback);
        sender.sendLog(fixture);
    }
}
