import com.dianping.logan.Logan;
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;

/** Actual HTTP sender + actual CLogan bytes. Only Android file scheduling is a host double. */
public final class WorkbenchReceiverUpload {
    public static void main(String[] args) throws Exception {
        Logan.fixture = new File(args[0]);
        String token = System.getenv("WORKBENCH_SDK_TOKEN");
        if (token == null || token.isEmpty()) throw new IllegalArgumentException("missing upload-only credential");
        final int[] status = {-999};
        final byte[][] reply = {null};
        WorkbenchLoganUpload.upload(args[1], "2026-10-19", "com.hxl.xlb.test", "triad-device",
            "1.0-triad", "triad-42", token, args[2], (code, data) -> {status[0]=code;reply[0]=data;});
        if (status[0] != 200 || reply[0] == null) throw new AssertionError("receiver rejected actual sender: " + status[0]);
        Files.write(Path.of(args[3]), reply[0]);
        System.out.println("Android host sender -> Go native receiver: accepted");
    }
}
