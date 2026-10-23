import com.dianping.logan.SendLogDefaultRunnable;
import java.io.File;
import java.util.HashMap;
import java.util.Map;

/** Actual HTTP sender; synthetic local redirect fixture only. */
public final class AndroidRedirectTest {
    public static void main(String[] args) {
        SendLogDefaultRunnable sender = new SendLogDefaultRunnable();
        sender.setUrl(args[0]);
        Map<String,String> headers = new HashMap<>();
        if (args[2].equals("credential")) headers.put("x-logan-upload-token", "synthetic-redirect-token");
        sender.setRequestHeader(headers);
        final int[] status = {-999};
        sender.setSendLogCallback((code, body) -> status[0] = code);
        sender.sendLog(new File(args[1]));
        if (status[0] != Integer.parseInt(args[3])) throw new AssertionError("unexpected redirect status " + status[0]);
    }
}
